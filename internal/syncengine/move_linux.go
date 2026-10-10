package syncengine

import "golang.org/x/sys/unix"

func nativeRename(from int, source string, to int, target string) error {
	return unix.Renameat2(from, source, to, target, unix.RENAME_NOREPLACE)
}
