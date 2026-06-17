package syncer

import (
	"io/fs"
	"path/filepath"
)

// prunedDirs are directory names skipped during a local walk: the digiKam trash
// and the culling staging folder, neither of which is part of the archive.
var prunedDirs = map[string]bool{".dtrash": true, "à trier": true}

// WalkLocal walks root and returns every regular file as a LocalFile with its
// path relative to root (slash-separated), size, and mtime (Unix seconds). The
// .dtrash and "à trier" directories are pruned.
func WalkLocal(root string) ([]LocalFile, error) {
	var files []LocalFile
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && prunedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, LocalFile{
			Rel:   filepath.ToSlash(rel),
			Size:  info.Size(),
			Mtime: info.ModTime().Unix(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
