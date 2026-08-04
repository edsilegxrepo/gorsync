package rsyncd

import (
	"fmt"
	"os"

	"github.com/edsilegxrepo/gorsync/internal/restrict"
)

func restrictToModules(modules []Module) error {
	var roDirs, rwDirs []string
	for _, mod := range modules {
		if mod.FS != nil || mod.WritableFS != nil {
			continue
		}
		if mod.Writable {
			if err := os.MkdirAll(mod.Path, 0o755); err != nil { // #nosec G301 -- module path directory creation permission (0755)
				return fmt.Errorf("MkdirAll(mod=%s): %v", mod.Name, err)
			}
			rwDirs = append(rwDirs, mod.Path)
		} else {
			roDirs = append(roDirs, mod.Path)
		}
	}
	return restrict.MaybeFileSystem(roDirs, rwDirs)
}
