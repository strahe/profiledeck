package switching

import (
	"os"

	"github.com/strahe/profiledeck/internal/profiletarget"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

const (
	targetBackendFile = switchtarget.BackendFile
)

type targetSpec = switchtarget.Spec

type targetSnapshot struct {
	Exists      bool
	IsSymlink   bool
	Fingerprint string
	Mode        os.FileMode
	Preview     TextPreview
	Content     string
	// privateLocator is backend-owned recovery state. It may be persisted only
	// in private backup data and must never cross a public output boundary.
	privateLocator string
}

func resolveFileTargetSpec(targetID, backendID, path, label string) (targetSpec, error) {
	return switchtarget.ResolveFileSpec(targetID, backendID, path, label)
}

func targetSnapshotFromSwitching(snapshot switchtarget.Snapshot) targetSnapshot {
	return targetSnapshot{
		Exists: snapshot.Exists, IsSymlink: snapshot.IsSymlink, Fingerprint: snapshot.Fingerprint,
		Mode: snapshot.Mode, Preview: textPreviewFromProfileTarget(snapshot.Preview), Content: snapshot.Content,
		privateLocator: snapshot.OpaqueState,
	}
}

func switchingSnapshotFromTarget(snapshot targetSnapshot) switchtarget.Snapshot {
	return switchtarget.Snapshot{
		Exists: snapshot.Exists, IsSymlink: snapshot.IsSymlink, Fingerprint: snapshot.Fingerprint,
		Mode: snapshot.Mode, Preview: profiletarget.Preview{Content: snapshot.Preview.Content, Truncated: snapshot.Preview.Truncated},
		Content: snapshot.Content, OpaqueState: snapshot.privateLocator,
	}
}
