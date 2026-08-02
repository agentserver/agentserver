package productionimage

import (
	"debug/buildinfo"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func validateLinuxGoExecutable(path, binaryName, platform string) error {
	if err := validateCanonicalFile("production Go executable "+binaryName, path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o111 == 0 || info.Size() < 1 || info.Size() > 256*1024*1024 {
		return fmt.Errorf("production Go executable %s has invalid mode or size", binaryName)
	}
	executable, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("open production Go executable %s as ELF: %w", binaryName, err)
	}
	defer executable.Close()
	wantMachine := elf.EM_AARCH64
	if platform == PlatformLinuxAMD64 {
		wantMachine = elf.EM_X86_64
	}
	if executable.Class != elf.ELFCLASS64 || executable.Data != elf.ELFDATA2LSB ||
		executable.Machine != wantMachine || executable.Type != elf.ET_EXEC {
		return fmt.Errorf("production Go executable %s must be a %s static executable", binaryName, platform)
	}
	for _, program := range executable.Progs {
		if program.Type == elf.PT_INTERP {
			return fmt.Errorf("production Go executable %s contains a dynamic interpreter", binaryName)
		}
	}
	libraries, err := executable.ImportedLibraries()
	if err != nil && !errors.Is(err, elf.ErrNoSymbols) {
		return fmt.Errorf("inspect production Go executable %s imports: %w", binaryName, err)
	}
	if len(libraries) != 0 {
		return fmt.Errorf("production Go executable %s imports dynamic libraries %v", binaryName, libraries)
	}
	information, err := buildinfo.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read production Go executable %s build information: %w", binaryName, err)
	}
	wantMain := "github.com/agentserver/agentserver/v2/cmd/" + sourceCommand(binaryName)
	if information.GoVersion != GoToolchain || information.Path != wantMain ||
		information.Main.Path != "github.com/agentserver/agentserver/v2" || filepath.Base(path) != binaryName {
		return fmt.Errorf("production Go executable %s has unexpected toolchain or main package", binaryName)
	}
	settings := make(map[string]string, len(information.Settings))
	for _, setting := range information.Settings {
		settings[setting.Key] = setting.Value
	}
	for key, value := range map[string]string{
		"GOOS": "linux", "GOARCH": platformArchitecture(platform), "CGO_ENABLED": "0", "-buildmode": "exe", "-trimpath": "true",
	} {
		if settings[key] != value {
			return fmt.Errorf("production Go executable %s build setting %s = %q, want %q", binaryName, key, settings[key], value)
		}
	}
	return nil
}
