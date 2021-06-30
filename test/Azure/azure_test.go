package test

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
)

func TestAzureE2E(t *testing.T) {
	ctx := context.TODO()
	deferDestroy := false

	t.Log("Running Terraform init and apply")
	terraformOpts := terraform.WithDefaultRetryableErrors(t, &terraform.Options{
		TerraformDir: "./terraform",
	})
	if deferDestroy {
		defer terraform.Destroy(t, terraformOpts)
	}
	terraform.InitAndApply(t, terraformOpts)
	kubeconfig := terraform.Output(t, terraformOpts, "aks_kube_config")
	aksHost := terraform.Output(t, terraformOpts, "aks_host")
	aksCert := terraform.Output(t, terraformOpts, "aks_client_certificate")
	aksKey := terraform.Output(t, terraformOpts, "aks_client_key")
	aksCa := terraform.Output(t, terraformOpts, "aks_cluster_ca_certificate")

	t.Log("Installing Flux")
	kubeconfigPath, kubeClient, err := getKubernetesCredentials(kubeconfig, aksHost, aksCert, aksKey, aksCa)
	require.NoError(t, err)
	defer os.RemoveAll(filepath.Dir(kubeconfigPath))
	err = installFlux(ctx, kubeconfigPath)
	require.NoError(t, err)
	//err = bootrapFlux(ctx, kubeClient)
	//require.NoError(t, err)

	t.Log("Verifying Flux installation")
	require.Eventually(t, func() bool {
		err := verifyGitAndKustomization(ctx, kubeClient, "flux-system", "flux-system")
		if err != nil {
			return false
		}
		return true
	}, 5*time.Second, 1*time.Second)

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
			ref:    "feature",
		},
		{
			name:   "https from 'v1' branch",
			scheme: "https",
			ref:    "v1",
		},
	}
	for _, tt := range applicationNsTest {
		t.Run(tt.name, func(t *testing.T) {
			require.Eventually(t, func() bool {
				namespace := fmt.Sprintf("application-gitops-%s-%s", tt.scheme, tt.ref)
				name := "application-gitops"
				err := verifyGitAndKustomization(ctx, kubeClient, namespace, name)
				if err != nil {
					return false
				}
				return true
			}, 5*time.Second, 1*time.Second)
		})
	}
}

// getKubernetesCredentials returns a path to a kubeconfig file and a kube client instance.
func getKubernetesCredentials(kubeconfig, aksHost, aksCert, aksKey, aksCa string) (string, client.Client, error) {
	tmpDir, err := ioutil.TempDir("", "*-azure-e2e")
	if err != nil {
		return "", nil, err
	}
	kubeconfigPath := fmt.Sprintf("%s/kubeconfig", tmpDir)
	os.WriteFile(kubeconfigPath, []byte(kubeconfig), 0750)
	kubeCfg := &rest.Config{
		Host: aksHost,
		TLSClientConfig: rest.TLSClientConfig{
			CertData: []byte(aksCert),
			KeyData:  []byte(aksKey),
			CAData:   []byte(aksCa),
		},
	}
	err = sourcev1.AddToScheme(scheme.Scheme)
	if err != nil {
		return "", nil, err
	}
	err = kustomizev1.AddToScheme(scheme.Scheme)
	if err != nil {
		return "", nil, err
	}
	kubeClient, err := client.New(kubeCfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return "", nil, err
	}
	return kubeconfigPath, kubeClient, nil
}

// installFlux adds the core Flux components to the cluster specified in the kubeconfig file.
func installFlux(ctx context.Context, kubeconfigPath string) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(timeoutCtx, "flux", "install", "--components-extra", "image-reflector-controller,image-automation-controller", "--kubeconfig", kubeconfigPath)
	_, err := cmd.Output()
	if err != nil {
		return err
	}
	return nil
}

// bootrapFlux adds gitrespository and kustomization resources to sync from a repository
func bootrapFlux(ctx context.Context, kubeClient client.Client) error {
	privateByte, err := os.ReadFile("../../../id_rsa")
	if err != nil {
		return err
	}
	publicByte, err := os.ReadFile("../../../id_rsa.pub")
	if err != nil {
		return err
	}
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flux-system",
			Namespace: "flux-system",
		},
		StringData: map[string]string{
			"identity":     string(privateByte),
			"identity.pub": string(publicByte),
			"known_hosts":  "ssh.dev.azure.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC7Hr1oTWqNqOlzGJOfGJ4NakVyIzf1rXYd4d7wo6jBlkLvCA4odBlL0mDUyZ0/QUfTTqeu+tm22gOsv+VrVTMk6vwRU75gY/y9ut5Mb3bR5BV58dKXyq9A9UeB5Cakehn5Zgm6x1mKoVyf+FFn26iYqXJRgzIZZcZ5V6hrE0Qg39kZm4az48o0AUbf6Sp4SLdvnuMa2sVNwHBboS7EJkm57XQPVU3/QpyNLHbWDdzwtrlS+ez30S3AdYhLKEOxAG8weOnyrtLJAUen9mTkol8oII1edf7mWWbWVf0nBmly21+nZcmCTISQBtdcyPaEno7fFQMDD26/s0lfKob4Kw8H",
		},
	}
	err = kubeClient.Create(ctx, &secret, &client.CreateOptions{})
	if err != nil {
		return err
	}

	source := &sourcev1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flux-system",
			Namespace: "flux-system",
		},
		Spec: sourcev1.GitRepositorySpec{
			GitImplementation: sourcev1.LibGit2Implementation,
			Reference: &sourcev1.GitRepositoryRef{
				Branch: "main",
			},
			SecretRef: &meta.LocalObjectReference{
				Name: "flux-system",
			},
			URL: "ssh://git@ssh.dev.azure.com/v3/flux-azure/e2e/fleet-infra",
		},
	}
	err = kubeClient.Create(ctx, source, &client.CreateOptions{})
	if err != nil {
		return err
	}

	kustomization := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flux-system",
			Namespace: "flux-system",
		},
		Spec: kustomizev1.KustomizationSpec{
			Path: "./clusters/prod",
			SourceRef: kustomizev1.CrossNamespaceSourceReference{
				Kind:      sourcev1.GitRepositoryKind,
				Name:      "flux-system",
				Namespace: "flux-system",
			},
		},
	}
	err = kubeClient.Create(ctx, kustomization, &client.CreateOptions{})
	if err != nil {
		return err
	}

	return nil
}

// verifyGitAndKustomization checks that the gitrespository and kustomization combination are working properly.
func verifyGitAndKustomization(ctx context.Context, kubeClient client.Client, namespace, name string) error {
	nn := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}
	source := &sourcev1.GitRepository{}
	err := kubeClient.Get(ctx, nn, source)
	if err != nil {
		return err
	}
	if apimeta.IsStatusConditionFalse(source.Status.Conditions, meta.ReadyCondition) {
		return err
	}
	kustomization := &kustomizev1.Kustomization{}
	err = kubeClient.Get(ctx, nn, kustomization)
	if err != nil {
		return err
	}
	if apimeta.IsStatusConditionFalse(kustomization.Status.Conditions, meta.ReadyCondition) {
		return err
	}
	return nil
}
