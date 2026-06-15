package domain

import (
	scerr "github.com/scality/go-errors"
)

const maxNameBytes = 255

// ValidateName rejects filenames/dirnames that would be unsafe or rejected by kDrive.
func ValidateName(name string) error {
	if name == "" {
		return scerr.Wrap(ErrValidation,
			scerr.WithDetail("name cannot be empty"),
		)
	}
	if len(name) > maxNameBytes {
		return scerr.Wrap(ErrValidation,
			scerr.WithDetailf("name exceeds %d bytes", maxNameBytes),
			scerr.WithProperty("length", len(name)),
		)
	}
	if name == "." || name == ".." {
		return scerr.Wrap(ErrValidation,
			scerr.WithDetailf("name %q is reserved", name),
			scerr.WithProperty("name", name),
		)
	}
	for i := 0; i < len(name); i++ {
		b := name[i]
		if b == '/' || b == '\x00' || b < 0x20 || b == 0x7f {
			return scerr.Wrap(ErrValidation,
				scerr.WithDetailf("name contains forbidden byte 0x%02x at index %d", b, i),
				scerr.WithProperty("name", name),
			)
		}
	}
	return nil
}

// ValidateFolderID rejects zero/negative folder IDs.
func ValidateFolderID(id int64) error {
	if id <= 0 {
		return scerr.Wrap(ErrValidation,
			scerr.WithDetail("folder id must be positive"),
			scerr.WithProperty("folder_id", id),
		)
	}
	return nil
}

// ValidateFileID rejects zero/negative file IDs.
func ValidateFileID(id int64) error {
	if id <= 0 {
		return scerr.Wrap(ErrValidation,
			scerr.WithDetail("file id must be positive"),
			scerr.WithProperty("file_id", id),
		)
	}
	return nil
}
