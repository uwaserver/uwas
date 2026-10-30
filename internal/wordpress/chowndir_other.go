//go:build !unix

package wordpress

import (
	"errors"
	"os"
)

func openDirNoFollow(webRoot, rel string) (*os.File, error) { return nil, errors.ErrUnsupported }

func isNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }
