//go:build unix

package main

import "os"

func writeTestFile(path string, contents []byte) error {
	return os.WriteFile(path, contents, 0o400)
}

func createTestSymlink(target, link string) error {
	return os.Symlink(target, link)
}
