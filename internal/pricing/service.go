package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/strahe/profiledeck/internal/store"
)

const (
	CatalogURL = "https://raw.githubusercontent.com/strahe/profiledeck/main/pricing/catalog.json"
	stateKey   = "pricing.catalog.state"
	checkEvery = 24 * time.Hour
)

type Status struct {
	Automatic           bool   `json:"automatic"`
	CatalogVersion      int64  `json:"catalog_version"`
	LastCheckedAtUnixMS int64  `json:"last_checked_at_unix_ms"`
	LastUpdatedAtUnixMS int64  `json:"last_updated_at_unix_ms"`
	LastError           string `json:"last_error,omitempty"`
}

type state struct {
	Automatic           *bool           `json:"automatic,omitempty"`
	Catalog             json.RawMessage `json:"catalog,omitempty"`
	LastCheckedAtUnixMS int64           `json:"last_checked_at_unix_ms"`
	LastUpdatedAtUnixMS int64           `json:"last_updated_at_unix_ms"`
	LastError           string          `json:"last_error,omitempty"`
}

type Service struct {
	stores store.Factory
	client *http.Client
	mu     sync.Mutex
}

func NewService(stores store.Factory) *Service {
	return &Service{stores: stores, client: &http.Client{
		Timeout:       3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("pricing redirect rejected") },
	}}
}

func (service *Service) Snapshot(ctx context.Context) (Catalog, error) {
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return Catalog{}, err
	}
	defer db.Close()
	saved, err := readState(ctx, db)
	if err != nil {
		return Catalog{}, err
	}
	return catalogFromState(saved)
}

func (service *Service) SnapshotInStore(ctx context.Context, db *store.Store) (Catalog, error) {
	saved, err := readState(ctx, db)
	if err != nil {
		return Catalog{}, err
	}
	return catalogFromState(saved)
}

func (service *Service) Status(ctx context.Context) (Status, error) {
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return Status{}, err
	}
	defer db.Close()
	saved, err := readState(ctx, db)
	if err != nil {
		return Status{}, err
	}
	return statusFromState(saved)
}

func (service *Service) SetAutomatic(ctx context.Context, enabled bool) (Status, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	db, err := service.stores.OpenHealthy(ctx, false)
	if err != nil {
		return Status{}, err
	}
	defer db.Close()
	var result Status
	err = db.WithTransaction(ctx, func(tx *store.Store) error {
		if err := tx.ReserveSettingForWrite(ctx, stateKey); err != nil {
			return err
		}
		saved, err := readState(ctx, tx)
		if err != nil {
			return err
		}
		saved.Automatic = &enabled
		if err := writeState(ctx, tx, saved); err != nil {
			return err
		}
		result, err = statusFromState(saved)
		return err
	})
	return result, err
}

func (service *Service) Check(ctx context.Context, force bool) (Status, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return Status{}, err
	}
	saved, err := readState(ctx, db)
	closeErr := db.Close()
	if err != nil {
		return Status{}, err
	}
	if closeErr != nil {
		return Status{}, closeErr
	}
	if !force && (!automatic(saved) || time.Since(time.UnixMilli(saved.LastCheckedAtUnixMS)) < checkEvery) {
		return statusFromState(saved)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, CatalogURL, nil)
	if err != nil {
		return Status{}, err
	}
	response, fetchErr := service.client.Do(request)
	errorCode := "unavailable"
	var fetched Catalog
	var raw []byte
	if fetchErr == nil {
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			fetchErr = errors.New("pricing feed unavailable")
		} else {
			raw, fetchErr = io.ReadAll(io.LimitReader(response.Body, MaxCatalogBytes+1))
			if fetchErr == nil {
				fetched, fetchErr = Parse(raw)
				errorCode = "invalid"
			}
		}
	}
	writable, err := service.stores.OpenHealthy(ctx, false)
	if err != nil {
		return Status{}, err
	}
	defer writable.Close()
	var result Status
	err = writable.WithTransaction(ctx, func(tx *store.Store) error {
		if err := tx.ReserveSettingForWrite(ctx, stateKey); err != nil {
			return err
		}
		current, err := readState(ctx, tx)
		if err != nil {
			return err
		}
		active, err := catalogFromState(current)
		if err != nil {
			return err
		}
		minimumVersion := active.CatalogVersion
		if len(current.Catalog) != 0 {
			if _, err := Parse(current.Catalog); err != nil {
				var savedVersion struct {
					CatalogVersion int64 `json:"catalog_version"`
				}
				if json.Unmarshal(current.Catalog, &savedVersion) != nil || savedVersion.CatalogVersion <= 0 {
					minimumVersion = 0
				} else if savedVersion.CatalogVersion > minimumVersion {
					minimumVersion = savedVersion.CatalogVersion
				}
			}
		}
		current.LastCheckedAtUnixMS = time.Now().UnixMilli()
		if fetchErr != nil {
			current.LastError = errorCode
		} else if minimumVersion == 0 || fetched.CatalogVersion < minimumVersion ||
			fetched.CatalogVersion == active.CatalogVersion && !sameCatalog(fetched, active) {
			current.LastError = "invalid"
		} else {
			current.LastError = ""
			if fetched.CatalogVersion > active.CatalogVersion {
				current.Catalog = raw
				current.LastUpdatedAtUnixMS = current.LastCheckedAtUnixMS
			}
		}
		if err := writeState(ctx, tx, current); err != nil {
			return err
		}
		result, err = statusFromState(current)
		return err
	})
	return result, err
}

func sameCatalog(a, b Catalog) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func automatic(saved state) bool { return saved.Automatic == nil || *saved.Automatic }

func catalogFromState(saved state) (Catalog, error) {
	bundled := Embedded()
	if len(saved.Catalog) == 0 {
		return bundled, nil
	}
	accepted, err := Parse(saved.Catalog)
	if err != nil {
		return bundled, nil
	}
	if bundled.CatalogVersion > accepted.CatalogVersion {
		return bundled, nil
	}
	return accepted, nil
}

func statusFromState(saved state) (Status, error) {
	catalog, err := catalogFromState(saved)
	if err != nil {
		return Status{}, err
	}
	lastError := saved.LastError
	if len(saved.Catalog) != 0 {
		if _, err := Parse(saved.Catalog); err != nil {
			lastError = "invalid"
		}
	}
	return Status{
		Automatic: automatic(saved), CatalogVersion: catalog.CatalogVersion,
		LastCheckedAtUnixMS: saved.LastCheckedAtUnixMS,
		LastUpdatedAtUnixMS: saved.LastUpdatedAtUnixMS, LastError: lastError,
	}, nil
}

func readState(ctx context.Context, db *store.Store) (state, error) {
	setting, err := db.GetSetting(ctx, stateKey)
	if errors.Is(err, store.ErrNotFound) {
		return state{}, nil
	}
	if err != nil {
		return state{}, err
	}
	var saved state
	if err := json.Unmarshal([]byte(setting.ValueJSON), &saved); err != nil {
		return state{}, err
	}
	if saved.LastCheckedAtUnixMS < 0 || saved.LastUpdatedAtUnixMS < 0 ||
		(saved.LastError != "" && saved.LastError != "unavailable" && saved.LastError != "invalid") {
		return state{}, errors.New("pricing state is invalid")
	}
	return saved, nil
}

func writeState(ctx context.Context, db *store.Store, saved state) error {
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	_, err = db.UpsertSetting(ctx, store.UpsertSettingParams{Key: stateKey, ValueJSON: strings.TrimSpace(string(data))})
	return err
}
