package store

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const checkpointLimit = 64 << 20

func allowedCheckpoint(name string) bool {
	return fs.ValidPath(name) && !strings.Contains(name, "\\") && (strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".mp3"))
}

// Pack includes only run JSON and cached audio, never .env or the process lock.
func Pack(directory string) ([]byte, error) {
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	var total int64
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)
		if !allowedCheckpoint(name) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("non-regular checkpoint file")
		}
		total += info.Size()
		if total > checkpointLimit {
			return errors.New("checkpoints exceed 64 MB; prune old run directories")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data)), ModTime: info.ModTime()}); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if os.IsNotExist(err) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func Unpack(directory string, data []byte) error {
	if info, err := os.Lstat(directory); err == nil {
		if !info.IsDir() {
			return errors.New("checkpoint destination must be a directory")
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			return errors.New("restore requires an empty state directory")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg || !allowedCheckpoint(header.Name) || header.Size < 0 {
			return errors.New("invalid checkpoint archive entry")
		}
		total += header.Size
		if total > checkpointLimit {
			return errors.New("checkpoint archive exceeds 64 MB")
		}
		body, err := io.ReadAll(io.LimitReader(tr, header.Size+1))
		if err != nil {
			return err
		}
		if int64(len(body)) != header.Size {
			return errors.New("truncated checkpoint")
		}
		destination := filepath.Join(directory, filepath.FromSlash(header.Name))
		if _, err := os.Lstat(destination); err == nil {
			return errors.New("refusing to overwrite local checkpoint; restore into an empty directory")
		}
		if err := Write(destination, body, 0600); err != nil {
			return err
		}
	}
}
