package test

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/hashicorp/terraform-exec/tfinstall"
	"github.com/stretchr/testify/require"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

var kubeconfigPath string
var kubeClient client.Client

func TestMain(m *testing.M) {
	ctx := context.TODO()
	teardown := false

	log.Println("Setting up Azure test infrastructure")
	execPath, err := tfinstall.Find(ctx, tfinstall.ExactPath("/usr/bin/terraform"))
	if err != nil {
		log.Fatalf("terraform exec path not found: %v", err)
	}
	tf, err := tfexec.NewTerraform("./terraform/aks-calico", execPath)
	if err != nil {
		log.Fatalf("could not create terraform instance: %v", err)
	}
	err = tf.Init(ctx, tfexec.Upgrade(true))
	if err != nil {
		log.Fatalf("error running init: %v", err)
	}
	err = tf.Apply(ctx)
	if err != nil {
		log.Fatalf("error running apply: %v", err)
	}
	state, err := tf.Show(ctx)
	if err != nil {
		log.Fatalf("error running show: %v", err)
	}
	outputs := state.Values.Outputs
	azdoPat := outputs["shared_pat"].Value.(string)
	idRsa := outputs["shared_id_rsa"].Value.(string)
	idRsaPub := outputs["shared_id_rsa_pub"].Value.(string)
	kubeconfig := outputs["aks_kube_config"].Value.(string)
	aksHost := outputs["aks_host"].Value.(string)
	aksCert := outputs["aks_client_certificate"].Value.(string)
	aksKey := outputs["aks_client_key"].Value.(string)
	aksCa := outputs["aks_cluster_ca_certificate"].Value.(string)

	log.Println("Creating Kubernetes client")
	kubeconfigPath, kubeClient, err = getKubernetesCredentials(kubeconfig, aksHost, aksCert, aksKey, aksCa)
	if err != nil {
		log.Fatalf("error create Kubernetes client: %v", err)
	}
	defer os.RemoveAll(filepath.Dir(kubeconfigPath))
	err = installFlux(ctx, kubeconfigPath)
	if err != nil {
		log.Fatalf("error installing Flux: %v", err)
	}
	err = bootrapFlux(ctx, kubeClient, azdoPat, idRsa, idRsaPub)
	if err != nil {
		log.Fatalf("error bootstrapping Flux: %v", err)
	}

	log.Println("Running Azure e2e tests")
	exitVal := m.Run()

	log.Println("Tearing down Azure test infrastructure")
	if teardown {
		err = tf.Destroy(ctx)
		if err != nil {
			log.Fatalf("error running Show: %v", err)
		}
	}

	os.Exit(exitVal)
}

func TestFluxInstallation(t *testing.T) {
	ctx := context.TODO()
	require.Eventually(t, func() bool {
		err := verifyGitAndKustomization(ctx, kubeClient, "flux-system", "flux-system")
		if err != nil {
			return false
		}
		return true
	}, 30*time.Second, 1*time.Second)
}

func TestAzureDevOpsCloning(t *testing.T) {
	ctx := context.TODO()
	t.Log("Verifying application-gitops namespaces")
	var applicationNsTest = []struct {
		name   string
		scheme string
		ref    string
	}{
		{
			name:   "https from 'main' branch",
			scheme: "https",
			ref:    "main",
		},
		{
			name:   "https from 'feature' branch",
			scheme: "https",
			ref:    "feature-branch",
		},
		{
			name:   "https from 'v1' branch",
			scheme: "https",
			ref:    "v1-tag",
		},
		{
			name:   "ssh from 'main' branch",
			scheme: "ssh",
			ref:    "main",
		},
		{
			name:   "ssh from 'feature' branch",
			scheme: "ssh",
			ref:    "feature-branch",
		},
		{
			name:   "ssh from 'v1' branch",
			scheme: "ssh",
			ref:    "v1-tag",
		},
	}
	for _, tt := range applicationNsTest {
		t.Run(tt.name, func(t *testing.T) {
			require.Eventually(t, func() bool {
				name := fmt.Sprintf("application-gitops-%s-%s", tt.scheme, tt.ref)
				namespace := "flux-system"
				err := verifyGitAndKustomization(ctx, kubeClient, namespace, name)
				if err != nil {
					return false
				}
				return true
			}, 30*time.Second, 1*time.Second)
		})
	}
}

func TestAzureDevOpsCommitStatus(t *testing.T) {
}

func TestEventHubNotification(t *testing.T) {
}

func TestACRImageUpdateList(t *testing.T) {
}

func TestACRImageUpdateCommit(t *testing.T) {
}

func TestACRHelmRelease(t *testing.T) {
}

func TestKeyVaultSops(t *testing.T) {
}
