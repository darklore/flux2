package test

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
)

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
func bootrapFlux(ctx context.Context, kubeClient client.Client, azdoPat, idRsa, idRsaPub string) error {
	sshCredentials := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "flux-system", Namespace: "flux-system"}}
	_, err := controllerutil.CreateOrUpdate(ctx, kubeClient, sshCredentials, func() error {
		sshCredentials.StringData = map[string]string{
			"identity":     idRsa,
			"identity.pub": idRsaPub,
			"known_hosts":  "ssh.dev.azure.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC7Hr1oTWqNqOlzGJOfGJ4NakVyIzf1rXYd4d7wo6jBlkLvCA4odBlL0mDUyZ0/QUfTTqeu+tm22gOsv+VrVTMk6vwRU75gY/y9ut5Mb3bR5BV58dKXyq9A9UeB5Cakehn5Zgm6x1mKoVyf+FFn26iYqXJRgzIZZcZ5V6hrE0Qg39kZm4az48o0AUbf6Sp4SLdvnuMa2sVNwHBboS7EJkm57XQPVU3/QpyNLHbWDdzwtrlS+ez30S3AdYhLKEOxAG8weOnyrtLJAUen9mTkol8oII1edf7mWWbWVf0nBmly21+nZcmCTISQBtdcyPaEno7fFQMDD26/s0lfKob4Kw8H",
		}
		return nil
	})
	if err != nil {
		return err
	}
	httpsCredentials := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "https-credentials", Namespace: "flux-system"}}
	_, err = controllerutil.CreateOrUpdate(ctx, kubeClient, httpsCredentials, func() error {
		httpsCredentials.StringData = map[string]string{
			"username": "git",
			"password": azdoPat,
		}
		return nil
	})
	if err != nil {
		return err
	}
	source := &sourcev1.GitRepository{ObjectMeta: metav1.ObjectMeta{Name: "flux-system", Namespace: "flux-system"}}
	_, err = controllerutil.CreateOrUpdate(ctx, kubeClient, source, func() error {
		source.Spec = sourcev1.GitRepositorySpec{
			GitImplementation: sourcev1.LibGit2Implementation,
			Reference: &sourcev1.GitRepositoryRef{
				Branch: "main",
			},
			SecretRef: &meta.LocalObjectReference{
				Name: "flux-system",
			},
			URL: "ssh://git@ssh.dev.azure.com/v3/flux-azure/e2e/fleet-infra",
		}
		return nil
	})
	if err != nil {
		return err
	}
	kustomization := &kustomizev1.Kustomization{ObjectMeta: metav1.ObjectMeta{Name: "flux-system", Namespace: "flux-system"}}
	_, err = controllerutil.CreateOrUpdate(ctx, kubeClient, kustomization, func() error {
		kustomization.Spec = kustomizev1.KustomizationSpec{
			Path: "./clusters/prod",
			SourceRef: kustomizev1.CrossNamespaceSourceReference{
				Kind:      sourcev1.GitRepositoryKind,
				Name:      "flux-system",
				Namespace: "flux-system",
			},
		}
		return nil
	})
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
	log.Println(nn)
	source := &sourcev1.GitRepository{}
	err := kubeClient.Get(ctx, nn, source)
	if err != nil {
		return err
	}
	if apimeta.IsStatusConditionPresentAndEqual(source.Status.Conditions, meta.ReadyCondition, metav1.ConditionTrue) == false {
		return fmt.Errorf("source condition not ready")
	}
	log.Println("source")
	kustomization := &kustomizev1.Kustomization{}
	err = kubeClient.Get(ctx, nn, kustomization)
	if err != nil {
		return err
	}
	if apimeta.IsStatusConditionPresentAndEqual(kustomization.Status.Conditions, meta.ReadyCondition, metav1.ConditionTrue) == false {
		return fmt.Errorf("kustomization condition not ready")
	}
	log.Println("kust")
	return nil
}
