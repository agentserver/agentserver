package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/productiondeploy"
	"github.com/agentserver/agentserver/v2/internal/productionimage"
)

const maximumImageManifestBytes = 1024 * 1024

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(arguments []string, stdout, stderr io.Writer) int {
	if stdout == nil || stderr == nil || len(arguments) == 0 {
		writeUsage(stderr)
		return 2
	}
	var err error
	switch arguments[0] {
	case "prepare":
		err = runPrepare(arguments[1:], stdout, stderr)
	case "verify-oci":
		err = runVerifyOCI(arguments[1:], stdout, stderr)
	case "verify-managed-release":
		err = runVerifyManagedRelease(arguments[1:], stdout, stderr)
	case "verify-tar":
		err = runVerifyTar(arguments[1:], stdout, stderr)
	default:
		writeUsage(stderr)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "agentserver-image %s: %v\n", arguments[0], err)
		return 1
	}
	return 0
}

func runVerifyManagedRelease(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("verify-managed-release", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var configPath, manifestPath, archivePath, harnessManifestPath, harnessArchivePath string
	flags.StringVar(&configPath, "config", "", "validated production deployment config")
	flags.StringVar(&manifestPath, "manifest", "", "external managed-sandbox image manifest")
	flags.StringVar(&archivePath, "archive", "", "saved managed-sandbox OCI image archive")
	flags.StringVar(&harnessManifestPath, "harness-manifest", "", "external harness image manifest")
	flags.StringVar(&harnessArchivePath, "harness-archive", "", "saved harness OCI image archive")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("verify-managed-release accepts no positional arguments")
	}
	config, err := productiondeploy.LoadConfig(configPath)
	if err != nil {
		return err
	}
	if !config.Document.Managed.Enabled {
		return errors.New("production deployment has managed executor disabled")
	}
	harnessManifest, harnessEvidence, err := loadVerifiedReleaseImage("harness", harnessManifestPath, harnessArchivePath)
	if err != nil {
		return err
	}
	managedManifest, managedEvidence, err := loadVerifiedReleaseImage("managed-sandbox", manifestPath, archivePath)
	if err != nil {
		return err
	}
	document := config.Document
	if err := productionimage.VerifyManagedReleaseLock(productionimage.ManagedReleaseArtifacts{
		HarnessManifest: harnessManifest, HarnessEvidence: harnessEvidence,
		ManagedSandboxManifest: managedManifest, ManagedSandboxEvidence: managedEvidence,
	}, productionimage.ManagedReleaseLock{
		Platform: document.Platform, HarnessImage: document.Images.Harness,
		ManagedSandboxImage: document.Images.ManagedSandbox,
		CLISHA256:           document.Managed.Lark.CLISHA256, SkillSHA256: document.Managed.Lark.SkillSHA256,
	}); err != nil {
		return err
	}
	fmt.Fprintf(
		stdout,
		"agentserver-image verify-managed-release: %s verified; harness=%s managed-sandbox=%s\n",
		managedManifest.Platform, harnessEvidence.ImageManifestDigest, managedEvidence.ImageManifestDigest,
	)
	return nil
}

func loadVerifiedReleaseImage(label, manifestPath, archivePath string) (productionimage.Manifest, productionimage.OCIImageEvidence, error) {
	manifestBytes, err := readDirectFile(label+" image manifest", manifestPath, maximumImageManifestBytes)
	if err != nil {
		return productionimage.Manifest{}, productionimage.OCIImageEvidence{}, err
	}
	manifest, err := productionimage.ParseManifest(manifestBytes)
	if err != nil {
		return productionimage.Manifest{}, productionimage.OCIImageEvidence{}, err
	}
	archive, err := openDirectFile(label+" OCI archive", archivePath)
	if err != nil {
		return productionimage.Manifest{}, productionimage.OCIImageEvidence{}, err
	}
	evidence, verifyErr := productionimage.VerifyImageOCIWithEvidence(archive, manifestBytes)
	closeErr := archive.Close()
	if verifyErr != nil || closeErr != nil {
		return productionimage.Manifest{}, productionimage.OCIImageEvidence{}, errors.Join(verifyErr, closeErr)
	}
	return manifest, evidence, nil
}

func runVerifyOCI(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("verify-oci", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var manifestPath, archivePath string
	flags.StringVar(&manifestPath, "manifest", "", "external production image manifest")
	flags.StringVar(&archivePath, "archive", "", "saved OCI image archive")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("verify-oci accepts no positional arguments")
	}
	manifest, err := readDirectFile("production image manifest", manifestPath, maximumImageManifestBytes)
	if err != nil {
		return err
	}
	archive, err := openDirectFile("production OCI archive", archivePath)
	if err != nil {
		return err
	}
	evidence, verifyErr := productionimage.VerifyImageOCIWithEvidence(archive, manifest)
	closeErr := archive.Close()
	if verifyErr != nil || closeErr != nil {
		return errors.Join(verifyErr, closeErr)
	}
	parsed, err := productionimage.ParseManifest(manifest)
	if err != nil {
		return err
	}
	fmt.Fprintf(
		stdout, "agentserver-image verify-oci: %s %s image verified; digest=%s\n",
		parsed.Kind, parsed.Platform, evidence.ImageManifestDigest,
	)
	return nil
}

func runPrepare(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("prepare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var config productionimage.PrepareConfig
	flags.StringVar(&config.Kind, "kind", "", "closed-world image kind")
	flags.StringVar(&config.Platform, "platform", "", "closed-world image platform")
	flags.StringVar(&config.SourceRevision, "source-revision", "", "agentserver Git revision")
	flags.StringVar(&config.BinaryDirectory, "binary-dir", "", "directory containing the exact binary set")
	flags.StringVar(&config.CodexExecutable, "codex", "", "pinned stock Codex artifact")
	flags.StringVar(&config.BwrapExecutable, "bwrap", "", "pinned stock bwrap artifact")
	flags.StringVar(&config.RequirementsFile, "requirements", "", "reviewed Codex system requirements")
	flags.StringVar(&config.LarkSkillFile, "lark-skill", "", "reviewed managed Lark skill")
	flags.StringVar(&config.OutputDirectory, "output", "", "new image payload directory")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("prepare accepts no positional arguments")
	}
	result, err := productionimage.Prepare(config)
	if err != nil {
		return err
	}
	fmt.Fprintf(
		stdout,
		"agentserver-image prepare: %s %s payload ready; rootfs=%s manifest=%s\n",
		result.Manifest.Kind, result.Manifest.Platform, result.RootFS, result.ManifestFile,
	)
	return nil
}

func runVerifyTar(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("verify-tar", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var manifestPath, tarPath string
	flags.StringVar(&manifestPath, "manifest", "", "external production image manifest")
	flags.StringVar(&tarPath, "tar", "", "exported container filesystem tar")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("verify-tar accepts no positional arguments")
	}
	manifest, err := readDirectFile("production image manifest", manifestPath, maximumImageManifestBytes)
	if err != nil {
		return err
	}
	archive, err := openDirectFile("production image tar", tarPath)
	if err != nil {
		return err
	}
	verifyErr := productionimage.VerifyImageTar(archive, manifest)
	closeErr := archive.Close()
	if verifyErr != nil || closeErr != nil {
		return errors.Join(verifyErr, closeErr)
	}
	parsed, err := productionimage.ParseManifest(manifest)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "agentserver-image verify-tar: %s %s rootfs verified\n", parsed.Kind, parsed.Platform)
	return nil
}

func readDirectFile(label, path string, maximum int64) ([]byte, error) {
	file, err := openDirectFile(label, path)
	if err != nil {
		return nil, err
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, maximum+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(fmt.Errorf("read %s", label), readErr, closeErr)
	}
	if len(contents) == 0 || int64(len(contents)) > maximum {
		return nil, fmt.Errorf("%s must contain between 1 and %d bytes", label, maximum)
	}
	return contents, nil
}

func openDirectFile(label, path string) (*os.File, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return nil, fmt.Errorf("%s path must be absolute and clean", label)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", label, err)
	}
	if resolved != path {
		return nil, fmt.Errorf("%s path must contain no symlink component", label)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("%s must be a direct regular file not writable by group or other", label)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, opened) || opened.Size() != info.Size() {
		_ = file.Close()
		return nil, errors.Join(fmt.Errorf("%s identity changed while opening", label), statErr)
	}
	return file, nil
}

func writeUsage(writer io.Writer) {
	if writer == nil {
		return
	}
	fmt.Fprintln(writer, "usage: agentserver-image prepare --kind=service --platform=linux-amd64|linux-arm64 --source-revision=GIT_SHA --binary-dir=/absolute/path --output=/absolute/new-directory")
	fmt.Fprintln(writer, "       agentserver-image prepare --kind=harness --platform=linux-amd64|linux-arm64 --source-revision=GIT_SHA --binary-dir=/absolute/path --codex=/absolute/path --bwrap=/absolute/path --requirements=/absolute/path --lark-skill=/absolute/SKILL.md --output=/absolute/new-directory")
	fmt.Fprintln(writer, "       agentserver-image prepare --kind=managed-sandbox --platform=linux-amd64 --source-revision=GIT_SHA --binary-dir=/absolute/path --lark-skill=/absolute/SKILL.md --output=/absolute/new-directory")
	fmt.Fprintln(writer, "       agentserver-image verify-oci --manifest=/absolute/image-manifest.json --archive=/absolute/image.oci.tar")
	fmt.Fprintln(writer, "       agentserver-image verify-managed-release --config=/absolute/production-config.json --harness-manifest=/absolute/harness-image-manifest.json --harness-archive=/absolute/harness-image.oci.tar --manifest=/absolute/managed-sandbox-image-manifest.json --archive=/absolute/managed-sandbox-image.oci.tar")
	fmt.Fprintln(writer, "       agentserver-image verify-tar --manifest=/absolute/image-manifest.json --tar=/absolute/rootfs.tar")
}
