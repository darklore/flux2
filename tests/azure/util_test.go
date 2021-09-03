package test

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	helmv2beta1 "github.com/fluxcd/helm-controller/api/v2beta1"
	reflectorv1beta1 "github.com/fluxcd/image-reflector-controller/api/v1beta1"
	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
	notiv1beta1 "github.com/fluxcd/notification-controller/api/v1beta1"
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
	err = helmv2beta1.AddToScheme(scheme.Scheme)
	if err != nil {
		return "", nil, err
	}
	err = reflectorv1beta1.AddToScheme(scheme.Scheme)
	if err != nil {
		return "", nil, err
	}
	err = notiv1beta1.AddToScheme(scheme.Scheme)
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
func installFlux(ctx context.Context, kubeClient client.Client, kubeconfigPath, idRsa, idRsaPub, azdoPat string) error {
	// Add git credentials to flux-system namespace
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

	// Install Flux and push files to git repository
	repoDir, err := cloneRepo(ctx, "ssh://git@ssh.dev.azure.com/v3/flux-azure/e2e/fleet-infra")
	if err != nil {
		return err
	}
	err = runCommand(ctx, repoDir, "mkdir -p ./clusters/e2e/flux-system")
	if err != nil {
		return err
	}
	err = runCommand(ctx, repoDir, "flux install --components-extra=\"image-reflector-controller,image-automation-controller\" --export > ./clusters/e2e/flux-system/gotk-components.yaml")
	if err != nil {
		return err
	}
	err = runCommand(ctx, repoDir, "flux create source git flux-system --git-implementation=libgit2 --url=ssh://git@ssh.dev.azure.com/v3/flux-azure/e2e/fleet-infra --branch=main --secret-ref=flux-system --interval=1m  --export > ./clusters/e2e/flux-system/gotk-sync.yaml")
	if err != nil {
		return err
	}
	err = runCommand(ctx, repoDir, "flux create kustomization flux-system --source=flux-system --path='./clusters/e2e' --prune=true --interval=1m --export >> ./clusters/e2e/flux-system/gotk-sync.yaml")
	if err != nil {
		return err
	}
	err = runCommand(ctx, repoDir, "if [ \"$(git status --porcelain)\" ]; then git add -A && git commit -m 'install flux with sync manifests'; fi;")
	if err != nil {
		return err
	}
	err = runCommand(ctx, repoDir, "git push")
	if err != nil {
		return err
	}
	err = runCommand(ctx, repoDir, fmt.Sprintf("kubectl --kubeconfig=%s apply -f ./clusters/e2e/flux-system/", kubeconfigPath))
	if err != nil {
		return err
	}
	return nil
}

func cloneRepo(ctx context.Context, repoUrl string) (string, error) {
	tmpDir, err := ioutil.TempDir("", "*-repository")
	if err != nil {
		return "", err
	}
	err = runCommand(ctx, tmpDir, fmt.Sprintf("git clone %s repo", repoUrl))
	if err != nil {
		return "", err
	}
	repoPath := filepath.Join(tmpDir, "repo")
	return repoPath, nil
}

func addFileToRepo(ctx context.Context, repoDir, branch, filePath, fileContent string) error {
	err := runCommand(ctx, repoDir, fmt.Sprintf("git checkout %s", branch))
	if err != nil {
		return err
	}
	err = runCommand(ctx, repoDir, "if [ \"$(git status --porcelain)\" ]; then git add -A && git commit -m 'add file' && git push; fi;")
	if err != nil {
		return err
	}
	return nil
}

func runCommand(ctx context.Context, dir, command string) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(timeoutCtx, "bash", "-c", command)
	cmd.Dir = dir
	_, err := cmd.Output()
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
	if apimeta.IsStatusConditionPresentAndEqual(source.Status.Conditions, meta.ReadyCondition, metav1.ConditionTrue) == false {
		return fmt.Errorf("source condition not ready")
	}
	kustomization := &kustomizev1.Kustomization{}
	err = kubeClient.Get(ctx, nn, kustomization)
	if err != nil {
		return err
	}
	if apimeta.IsStatusConditionPresentAndEqual(kustomization.Status.Conditions, meta.ReadyCondition, metav1.ConditionTrue) == false {
		return fmt.Errorf("kustomization condition not ready")
	}
	return nil
}

func getTestManifest(namespace string) string {
	return fmt.Sprintf(`
apiVersion: v1
kind: Namespace
metadata:
  name: %s
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: foobar
  namespace: %s
`, namespace, namespace)
}
