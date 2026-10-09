package adapter

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// Opt-in publication canary. The loopback kubectl proxy supplies operator
// authentication. Only newly generated synthetic session resources are touched;
// no workspace credential or existing session is involved.
func TestLiveRepositoryResourceRoundTrip(t *testing.T) {
	origin := os.Getenv("AGENTSERVER_REPOSITORY_KUBE_PROXY")
	if origin == "" {
		t.Skip("requires an explicitly configured local Kubernetes proxy")
	}
	if origin != "http://127.0.0.1:18571" && origin != "http://127.0.0.1:18572" {
		t.Fatal("publication canary requires a dedicated loopback proxy")
	}
	region := os.Getenv("AGENTSERVER_REPOSITORY_KUBE_REGION")
	if !supportedRegion(region) {
		t.Fatal("explicit cn/sg region required")
	}
	kube, err := dynamic.NewForConfig(&rest.Config{Host: origin})
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(kube, &runtimeRecorder{}, Config{Namespace: "agentserver-sandboxes", Pool: "managed-cli-v1", Region: region, Scope: region + "-managed-cli", RuntimePort: 8443, RepositoryStorageClass: "longhorn", RepositoryStorageSize: "1Gi"})
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
	i := createIdentity{WorkspaceID: id, SessionID: id, EnvironmentID: id, Region: region, Scope: region + "-managed-cli"}
	volumeName := "as-session-" + strings.ReplaceAll(id, "-", "")
	poolName := p.repositoryPoolName(id)
	if _, err := p.resource(PersistentVolumeClaims).Get(t.Context(), volumeName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatal("canary PVC must not already exist")
	}
	t.Cleanup(func() {
		// Deleting this synthetic volume also collects its owned template/pool.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := p.resource(PersistentVolumeClaims).Delete(ctx, volumeName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("cleanup %s: %v", volumeName, err)
		}
	})
	for pass := 0; pass < 2; pass++ {
		actual, err := p.ensureRepositoryPool(t.Context(), i)
		if err != nil || actual != poolName {
			t.Fatalf("pass %d: pool=%q err=%v", pass, actual, err)
		}
	}
	t.Logf("%s real API create and idempotent reuse passed: %s", region, poolName)
}
