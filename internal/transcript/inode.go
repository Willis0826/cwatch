package transcript

import (
	"os"
	"syscall"
)

// inode returns the inode number of a file, or 0 when it is not available.
func inode(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}
