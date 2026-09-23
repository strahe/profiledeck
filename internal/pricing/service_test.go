package pricing

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/store"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func pricingTestService(t *testing.T) *Service {
	t.Helper()
	ctx := context.Background()
	factory := store.NewFactory(filepath.Join(t.TempDir(), "profiledeck.db"))
	db, err := factory.Open(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return NewService(factory)
}

func TestPricingCheckAcceptsNewCatalogAndKeepsItOnInvalidFeed(t *testing.T) {
	ctx := context.Background()
	service := pricingTestService(t)
	catalog := Embedded()
	catalog.CatalogVersion++
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	service.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != CatalogURL {
			t.Fatalf("unexpected pricing URL: %s", req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
	})
	status, err := service.Check(ctx, true)
	if err != nil || status.CatalogVersion != 2 || status.LastError != "" {
		t.Fatalf("accepted status = %+v, err = %v", status, err)
	}
	service.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString("{}")), Header: make(http.Header)}, nil
	})
	status, err = service.Check(ctx, true)
	if err != nil || status.CatalogVersion != 2 || status.LastError != "invalid" {
		t.Fatalf("rejected status = %+v, err = %v", status, err)
	}
	snapshot, err := service.Snapshot(ctx)
	if err != nil || snapshot.CatalogVersion != 2 {
		t.Fatalf("retained snapshot = %+v, err = %v", snapshot, err)
	}
}

func TestPricingAutomaticCheckCanBeDisabled(t *testing.T) {
	ctx := context.Background()
	service := pricingTestService(t)
	if _, err := service.SetAutomatic(ctx, false); err != nil {
		t.Fatal(err)
	}
	service.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("disabled automatic check made a request")
		return nil, nil
	})
	status, err := service.Check(ctx, false)
	if err != nil || status.Automatic || status.LastCheckedAtUnixMS != 0 {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
	service.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	_, _ = service.Check(requestCtx, true)
	if snapshot, err := service.Snapshot(ctx); err != nil || snapshot.CatalogVersion != 1 {
		t.Fatalf("fallback after timeout = %+v, err = %v", snapshot, err)
	}
}

func TestPricingCheckRejectsRollbackAndChangedSameVersion(t *testing.T) {
	ctx := context.Background()
	service := pricingTestService(t)
	catalog := Embedded()
	catalog.CatalogVersion = 2
	feed, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	service.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(feed)), Header: make(http.Header)}, nil
	})
	if status, err := service.Check(ctx, true); err != nil || status.CatalogVersion != 2 {
		t.Fatalf("initial update = %+v, err = %v", status, err)
	}
	feed = marshalCatalog(t, Embedded())
	if status, err := service.Check(ctx, true); err != nil || status.CatalogVersion != 2 || status.LastError != "invalid" {
		t.Fatalf("rollback = %+v, err = %v", status, err)
	}
	catalog.Entries[0].ShortContext.Input = "999"
	feed = marshalCatalog(t, catalog)
	if status, err := service.Check(ctx, true); err != nil || status.CatalogVersion != 2 || status.LastError != "invalid" {
		t.Fatalf("same-version modification = %+v, err = %v", status, err)
	}
}

func TestPricingUnreadableSavedCatalogKeepsSyncAvailableAndCanRecover(t *testing.T) {
	ctx := context.Background()
	service := pricingTestService(t)
	db, err := service.stores.OpenHealthy(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := state{Catalog: json.RawMessage(`{"catalog_version":2,"entries":"unreadable"}`)}
	if err := writeState(ctx, db, corrupt); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if catalog, err := service.Snapshot(ctx); err != nil || catalog.CatalogVersion != Embedded().CatalogVersion {
		t.Fatalf("offline catalog = %+v, err = %v", catalog, err)
	}
	if status, err := service.SetAutomatic(ctx, false); err != nil || status.Automatic || status.LastError != "invalid" {
		t.Fatalf("setting with unreadable catalog = %+v, err = %v", status, err)
	}
	feed := marshalCatalog(t, Embedded())
	service.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(feed)), Header: make(http.Header)}, nil
	})
	if status, err := service.Check(ctx, true); err != nil || status.LastError != "invalid" {
		t.Fatalf("older feed replaced unreadable accepted version: %+v, err = %v", status, err)
	}
	version2 := Embedded()
	version2.CatalogVersion = 2
	feed = marshalCatalog(t, version2)
	service.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(feed)), Header: make(http.Header)}, nil
	})
	if status, err := service.Check(ctx, true); err != nil || status.CatalogVersion != 2 || status.LastError != "" || status.Automatic {
		t.Fatalf("recovered status = %+v, err = %v", status, err)
	}
}

func marshalCatalog(t *testing.T, catalog Catalog) []byte {
	t.Helper()
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
