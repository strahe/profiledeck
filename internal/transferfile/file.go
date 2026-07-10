package transferfile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

var (
	ErrExists     = errors.New("transfer file already exists")
	ErrNotPrivate = errors.New("transfer file permissions are not private")
	ErrNotRegular = errors.New("transfer path is not a regular file")
	ErrChanged    = errors.New("transfer file changed")
	ErrTooLarge   = errors.New("transfer file is too large")
)

type ReadResult struct {
	Content []byte
	SHA256  string
	Mode    os.FileMode
}

type WriteRequest struct {
	Path      string
	Content   []byte
	Overwrite bool
	Mode      os.FileMode
	MaxBytes  int64
}

type WriteResult struct {
	SHA256 string
	Mode   os.FileMode
}

type fileSnapshot struct {
	Exists bool
	Info   os.FileInfo
	SHA256 string
}

func ReadPrivate(ctx context.Context, path string, maxBytes int64) (ReadResult, error) {
	if maxBytes <= 0 {
		return ReadResult{}, errors.New("maximum transfer file size must be positive")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return ReadResult{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return ReadResult{}, ErrNotRegular
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return ReadResult{}, ErrNotPrivate
	}
	if info.Size() > maxBytes {
		return ReadResult{}, ErrTooLarge
	}

	file, err := os.Open(path)
	if err != nil {
		return ReadResult{}, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return ReadResult{}, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return ReadResult{}, ErrChanged
	}

	var out bytes.Buffer
	hash := sha256.New()
	read, err := io.Copy(io.MultiWriter(&out, hash), io.LimitReader(contextReader{ctx: ctx, reader: file}, maxBytes+1))
	if err != nil {
		return ReadResult{}, err
	}
	if read > maxBytes {
		return ReadResult{}, ErrTooLarge
	}
	finalInfo, err := file.Stat()
	if err != nil {
		return ReadResult{}, err
	}
	if finalInfo.Size() != openedInfo.Size() || !finalInfo.ModTime().Equal(openedInfo.ModTime()) {
		return ReadResult{}, ErrChanged
	}
	return ReadResult{Content: out.Bytes(), SHA256: hex.EncodeToString(hash.Sum(nil)), Mode: info.Mode()}, nil
}

func WritePrivateAtomic(ctx context.Context, req WriteRequest) (WriteResult, error) {
	if req.MaxBytes <= 0 {
		return WriteResult{}, errors.New("maximum transfer file size must be positive")
	}
	if int64(len(req.Content)) > req.MaxBytes {
		return WriteResult{}, ErrTooLarge
	}
	mode := req.Mode.Perm()
	if mode == 0 {
		mode = 0o600
	}
	if runtime.GOOS != "windows" && mode != 0o600 {
		return WriteResult{}, fmt.Errorf("private transfer file mode must be 0600")
	}

	parent := filepath.Dir(req.Path)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return WriteResult{}, err
	}
	if !parentInfo.IsDir() {
		return WriteResult{}, fmt.Errorf("transfer file parent is not a directory")
	}
	expected, err := inspectSnapshot(ctx, req.Path, req.MaxBytes)
	if err != nil {
		return WriteResult{}, err
	}
	if expected.Exists && !req.Overwrite {
		return WriteResult{}, ErrExists
	}

	temp, err := os.CreateTemp(parent, ".profiledeck-export-*")
	if err != nil {
		return WriteResult{}, err
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		if temp != nil {
			_ = temp.Close()
		}
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(mode); err != nil {
		return WriteResult{}, err
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temp, hash), contextReader{ctx: ctx, reader: bytes.NewReader(req.Content)}); err != nil {
		return WriteResult{}, err
	}
	if err := temp.Sync(); err != nil {
		return WriteResult{}, err
	}
	if err := temp.Close(); err != nil {
		return WriteResult{}, err
	}
	temp = nil
	if err := verifySnapshot(ctx, req.Path, expected, req.MaxBytes); err != nil {
		return WriteResult{}, err
	}
	if err := os.Rename(tempPath, req.Path); err != nil {
		return WriteResult{}, err
	}
	removeTemp = false
	if err := os.Chmod(req.Path, mode); err != nil {
		return WriteResult{}, err
	}
	finalInfo, err := os.Lstat(req.Path)
	if err != nil {
		return WriteResult{}, err
	}
	if finalInfo.Mode()&os.ModeSymlink != 0 || !finalInfo.Mode().IsRegular() {
		return WriteResult{}, ErrNotRegular
	}
	if runtime.GOOS != "windows" && finalInfo.Mode().Perm() != 0o600 {
		return WriteResult{}, ErrNotPrivate
	}
	syncParentBestEffort(parent)
	return WriteResult{SHA256: hex.EncodeToString(hash.Sum(nil)), Mode: finalInfo.Mode()}, nil
}

func inspectSnapshot(ctx context.Context, path string, maxBytes int64) (fileSnapshot, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileSnapshot{}, nil
	}
	if err != nil {
		return fileSnapshot{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fileSnapshot{}, ErrNotRegular
	}
	read, err := readForSnapshot(ctx, path, maxBytes)
	if err != nil {
		return fileSnapshot{}, err
	}
	return fileSnapshot{Exists: true, Info: info, SHA256: read.SHA256}, nil
}

func readForSnapshot(ctx context.Context, path string, maxBytes int64) (ReadResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return ReadResult{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ReadResult{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		if info.Size() > maxBytes {
			return ReadResult{}, ErrTooLarge
		}
		return ReadResult{}, ErrNotRegular
	}
	hash := sha256.New()
	read, err := io.Copy(hash, io.LimitReader(contextReader{ctx: ctx, reader: file}, maxBytes+1))
	if err != nil {
		return ReadResult{}, err
	}
	if read > maxBytes {
		return ReadResult{}, ErrTooLarge
	}
	return ReadResult{SHA256: hex.EncodeToString(hash.Sum(nil)), Mode: info.Mode()}, nil
}

func verifySnapshot(ctx context.Context, path string, expected fileSnapshot, maxBytes int64) error {
	current, err := inspectSnapshot(ctx, path, maxBytes)
	if err != nil {
		return err
	}
	if current.Exists != expected.Exists {
		return ErrChanged
	}
	if !current.Exists {
		return nil
	}
	if !os.SameFile(current.Info, expected.Info) || current.SHA256 != expected.SHA256 {
		return ErrChanged
	}
	return nil
}

func syncParentBestEffort(parent string) {
	dir, err := os.Open(parent)
	if err != nil {
		return
	}
	defer dir.Close()
	_ = dir.Sync()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if r.ctx != nil {
		select {
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		default:
		}
	}
	return r.reader.Read(buffer)
}
