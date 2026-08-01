package llmproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maximumUpstreamCredentialBytes = 16 * 1024

// FileCredentialSource reads one complete upstream authentication header value
// from a restricted Secret projection for every authorized request. Reopening
// the path is deliberate: Kubernetes and CSI Secret rotations publish a new
// file identity atomically, and a long-lived file descriptor would pin the old
// credential. The credential is never accepted through process argv or env.
type FileCredentialSource struct {
	path       string
	headerName string
}

func NewFileCredentialSource(path, headerName string) (*FileCredentialSource, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return nil, errors.New("llmproxy upstream credential path must be absolute and clean")
	}
	if !validUpstreamCredential(UpstreamCredential{HeaderName: headerName, HeaderValue: "validation"}) {
		return nil, errors.New("llmproxy upstream credential header must be Authorization or api-key")
	}
	return &FileCredentialSource{path: path, headerName: headerName}, nil
}

func (source *FileCredentialSource) Credential(ctx context.Context, _ Principal) (_ UpstreamCredential, returnErr error) {
	if ctx == nil {
		return UpstreamCredential{}, errors.New("llmproxy upstream credential context is required")
	}
	if err := ctx.Err(); err != nil {
		return UpstreamCredential{}, err
	}
	if source == nil || source.path == "" || source.headerName == "" {
		return UpstreamCredential{}, errors.New("llmproxy upstream credential source is unavailable")
	}
	file, err := os.Open(source.path)
	if err != nil {
		return UpstreamCredential{}, errors.New("open llmproxy upstream credential")
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, errors.New("close llmproxy upstream credential"))
		}
	}()
	before, err := file.Stat()
	if err != nil {
		return UpstreamCredential{}, errors.New("inspect llmproxy upstream credential")
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0o077 != 0 ||
		before.Size() < 1 || before.Size() > maximumUpstreamCredentialBytes {
		return UpstreamCredential{}, fmt.Errorf(
			"llmproxy upstream credential must be a restricted regular file between 1 and %d bytes",
			maximumUpstreamCredentialBytes,
		)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumUpstreamCredentialBytes+1))
	if err != nil {
		return UpstreamCredential{}, errors.New("read llmproxy upstream credential")
	}
	defer clear(raw)
	after, err := file.Stat()
	if err != nil {
		return UpstreamCredential{}, errors.New("reinspect llmproxy upstream credential")
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() ||
		!before.ModTime().Equal(after.ModTime()) || int64(len(raw)) != before.Size() {
		return UpstreamCredential{}, errors.New("llmproxy upstream credential changed while it was being read")
	}
	credential := UpstreamCredential{HeaderName: source.headerName, HeaderValue: string(raw)}
	if !validUpstreamCredential(credential) {
		return UpstreamCredential{}, errors.New("llmproxy upstream credential file contains an invalid header value")
	}
	return credential, nil
}
