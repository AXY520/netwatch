package probe

import (
	"os"
	"path/filepath"
)

func firstExistingDir(paths ...string) string {
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return ""
}

func firstExistingFile(paths ...string) string {
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func firstReadableFile(paths ...string) string {
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		_ = f.Close()
		return p
	}
	return ""
}

func systemCgroupV2Root() string {
	for _, root := range []string{"/host/sys/fs/cgroup", "/sys/fs/cgroup"} {
		if firstReadableFile(filepath.Join(root, "cgroup.controllers")) != "" {
			return root
		}
	}
	return ""
}

func systemBPFRoot() string {
	return firstExistingDir("/host/sys/fs/bpf", "/sys/fs/bpf")
}
