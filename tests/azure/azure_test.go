package test

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	//eventhub "github.com/Azure/azure-event-hubs-go/v3"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/hashicorp/terraform-exec/tfinstall"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	//"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	//notiv1beta1 "github.com/fluxcd/notification-controller/api/v1beta1"
	//helmv2beta1 "github.com/fluxcd/helm-controller/api/v2beta1"
	reflectorv1beta1 "github.com/fluxcd/image-reflector-controller/api/v1beta1"
	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
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
	tf, err := tfexec.NewTerraform("./terraform/aks", execPath)
	if err != nil {
		log.Fatalf("could not create terraform instance: %v", err)
	}
	err = tf.Init(ctx, tfexec.Upgrade(true))
	if err != nil {
		log.Fatalf("error running init: %v", err)
	}
	/*err = tf.Apply(ctx)
	if err != nil {
		log.Fatalf("error running apply: %v", err)
	}*/
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
	err = installFlux(ctx, kubeClient, kubeconfigPath, idRsa, idRsaPub, azdoPat)
	if err != nil {
		log.Fatalf("error installing Flux: %v", err)
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

	tests := []struct {
		name      string
		url       string
		secretRef string
		branch    string
		tag       string
	}{
		{
			name:      "https-feature-branch",
			url:       "https://flux-azure@dev.azure.com/flux-azure/e2e/_git/application-gitops",
			secretRef: "https-credentials",
			branch:    "feature/branch",
		},
		{
			name:      "https-v1",
			url:       "https://flux-azure@dev.azure.com/flux-azure/e2e/_git/application-gitops",
			secretRef: "https-credentials",
			tag:       "v1",
		},
		{
			name:      "ssh-feature-branch",
			url:       "ssh://git@ssh.dev.azure.com/v3/flux-azure/e2e/application-gitops",
			secretRef: "flux-system",
			branch:    "feature/branch",
		},
		{
			name:      "ssh-v1",
			url:       "ssh://git@ssh.dev.azure.com/v3/flux-azure/e2e/application-gitops",
			secretRef: "flux-system",
			tag:       "v1",
		},
	}

	t.Log("Creating application sources")
	tmpDir, err := ioutil.TempDir("", "*-cloning-test")
	require.NoError(t, err)
	err = runCommand(ctx, tmpDir, "bash", "-c", "git clone ssh://git@ssh.dev.azure.com/v3/flux-azure/e2e/application-gitops")
	require.NoError(t, err)
	repoPath := filepath.Join(tmpDir, "application-gitops")
	err = runCommand(ctx, repoPath, "bash", "-c", "git checkout feature/branch")
	require.NoError(t, err)
	for _, tt := range tests {
		err = runCommand(ctx, repoPath, "bash", "-c", fmt.Sprintf("mkdir -p ./cloning-test/%s", tt.name))
		require.NoError(t, err)
		err = runCommand(ctx, repoPath, "bash", "-c", fmt.Sprintf("echo '%s' > ./cloning-test/%s/configmap.yaml", getTestManifest(tt.name), tt.name))
		require.NoError(t, err)
	}
	err = runCommand(ctx, repoPath, "bash", "-c", "if [ -z '$(git status --porcelain)' ]; then git add -A && git commit -m 'add application test' && git tag -d v1 && git tag v1; fi;")
	require.NoError(t, err)
	err = runCommand(ctx, repoPath, "bash", "-c", "git push --tags && git push")
	require.NoError(t, err)

	t.Log("Verifying application-gitops namespaces")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &sourcev1.GitRepository{ObjectMeta: metav1.ObjectMeta{Name: tt.name, Namespace: "flux-system"}}
			_, err := controllerutil.CreateOrUpdate(ctx, kubeClient, source, func() error {
				source.Spec = sourcev1.GitRepositorySpec{
					GitImplementation: sourcev1.LibGit2Implementation,
					Reference: &sourcev1.GitRepositoryRef{
						Branch: tt.branch,
						Tag:    tt.tag,
					},
					SecretRef: &meta.LocalObjectReference{
						Name: tt.secretRef,
					},
					URL: tt.url,
				}
				return nil
			})
			require.NoError(t, err)
			kustomization := &kustomizev1.Kustomization{ObjectMeta: metav1.ObjectMeta{Name: tt.name, Namespace: "flux-system"}}
			_, err = controllerutil.CreateOrUpdate(ctx, kubeClient, kustomization, func() error {
				kustomization.Spec = kustomizev1.KustomizationSpec{
					Path: fmt.Sprintf("./cloning-test/%s", tt.name),
					SourceRef: kustomizev1.CrossNamespaceSourceReference{
						Kind:      sourcev1.GitRepositoryKind,
						Name:      tt.name,
						Namespace: "flux-system",
					},
					Interval: metav1.Duration{Duration: 1 * time.Minute},
					Prune:    true,
				}
				return nil
			})
			require.NoError(t, err)

			// wait for deployment
			require.Eventually(t, func() bool {
				namespace := "flux-system"
				err := verifyGitAndKustomization(ctx, kubeClient, namespace, tt.name)
				if err != nil {
					return false
				}
				return true
			}, 10*time.Second, 1*time.Second)
		})
	}
}

func TestImageRepositoryACR(t *testing.T) {
	ctx := context.TODO()

	// Create namespace for test
	namespace := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "acr-image-update-list",
		},
	}
	err := kubeClient.Create(ctx, &namespace)
	require.NoError(t, err)
	defer func() {
		kubeClient.Delete(ctx, &namespace)
		require.NoError(t, err)
	}()

	// Copy ACR credentials to new namespace
	acrNn := types.NamespacedName{
		Name:      "acr-docker",
		Namespace: "flux-system",
	}
	acrSecret := corev1.Secret{}
	err = kubeClient.Get(ctx, acrNn, &acrSecret)
	require.NoError(t, err)
	acrSecret.ObjectMeta = metav1.ObjectMeta{
		Name:      acrNn.Name,
		Namespace: namespace.Name,
	}
	err = kubeClient.Create(ctx, &acrSecret)
	require.NoError(t, err)

	// Create image repository
	imageRepository := reflectorv1beta1.ImageRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "podinfo",
			Namespace: namespace.Name,
		},
		Spec: reflectorv1beta1.ImageRepositorySpec{
			Image: "acrappsoarfish.azurecr.io/container/podinfo",
			Interval: metav1.Duration{
				Duration: 1 * time.Minute,
			},
			SecretRef: &meta.LocalObjectReference{
				Name: acrSecret.Name,
			},
		},
	}
	err = kubeClient.Create(ctx, &imageRepository)
	require.NoError(t, err)

	// Wait for image repository to be ready
	require.Eventually(t, func() bool {
		nn := types.NamespacedName{
			Name:      imageRepository.Name,
			Namespace: imageRepository.Namespace,
		}
		checkIr := reflectorv1beta1.ImageRepository{}
		err := kubeClient.Get(ctx, nn, &checkIr)
		if err != nil {
			return false
		}
		if apimeta.IsStatusConditionFalse(checkIr.Status.Conditions, meta.ReadyCondition) {
			return false
		}
		if checkIr.Status.LastScanResult.TagCount == 0 {
			return false
		}
		return true
	}, 30*time.Second, 1*time.Second)

	// Check that the change has been comitted and changed
}

func TestKeyVaultSops(t *testing.T) {
	// Create encrypted secret with
	//

}

/*func TestAzureDevOpsCommitStatus(t *testing.T) {
	ctx := context.TODO()

	// Create namespace for test
	namespace := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "event-hub-notifications",
		},
	}
	err := kubeClient.Create(ctx, &namespace)
	require.NoError(t, err)
	defer func() {
		kubeClient.Delete(ctx, &namespace)
		require.NoError(t, err)
	}()

	// Copy ACR credentials to new namespace
	eventHubNn := types.NamespacedName{
		Name:      "azure-event-hub-sas",
		Namespace: "flux-system",
	}
	eventHubSecret := corev1.Secret{}
	err = kubeClient.Get(ctx, eventHubNn, &eventHubSecret)
	require.NoError(t, err)
	eventHubSecret.ObjectMeta = metav1.ObjectMeta{
		Name:      eventHubNn.Name,
		Namespace: namespace.Name,
	}
	err = kubeClient.Create(ctx, &eventHubSecret)
	require.NoError(t, err)

	// Create event hub provider
	provider := notiv1beta1.Provider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "event-hub",
			Namespace: namespace.Name,
		},
		Spec: notiv1beta1.ProviderSpec{
			Type:    "azureeventhub",
			Channel: "flux",
			SecretRef: &meta.LocalObjectReference{
				Name: eventHubSecret.Name,
			},
		},
	}
	err = kubeClient.Create(ctx, &provider)
	require.NoError(t, err)

	// Create alert
	alert := notiv1beta1.Alert{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "event-hub",
			Namespace: namespace.Name,
		},
		Spec: notiv1beta1.AlertSpec{
			ProviderRef: meta.LocalObjectReference{
				Name: provider.Name,
			},
			EventSources: []notiv1beta1.CrossNamespaceObjectReference{
				{
					Kind:      "GitRepository",
					Name:      "flux-system",
					Namespace: "flux-system",
				},
			},
		},
	}
	err = kubeClient.Create(ctx, &alert)
	require.NoError(t, err)
}*/

/*func TestEventHubNotification(t *testing.T) {
	ctx := context.TODO()

	// Create namespace for test
	namespace := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "event-hub-notifications",
		},
	}
	err := kubeClient.Create(ctx, &namespace)
	require.NoError(t, err)
	defer func() {
		kubeClient.Delete(ctx, &namespace)
		require.NoError(t, err)
	}()

	// Copy ACR credentials to new namespace
	eventHubNn := types.NamespacedName{
		Name:      "azure-event-hub-sas",
		Namespace: "flux-system",
	}
	eventHubSecret := corev1.Secret{}
	err = kubeClient.Get(ctx, eventHubNn, &eventHubSecret)
	require.NoError(t, err)
	eventHubSecret.ObjectMeta = metav1.ObjectMeta{
		Name:      eventHubNn.Name,
		Namespace: namespace.Name,
	}
	err = kubeClient.Create(ctx, &eventHubSecret)
	require.NoError(t, err)

	// Create event hub provider
	provider := notiv1beta1.Provider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "event-hub",
			Namespace: namespace.Name,
		},
		Spec: notiv1beta1.ProviderSpec{
			Type:    "azureeventhub",
			Channel: "flux",
			SecretRef: &meta.LocalObjectReference{
				Name: eventHubSecret.Name,
			},
		},
	}
	err = kubeClient.Create(ctx, &provider)
	require.NoError(t, err)

	// Create alert
	alert := notiv1beta1.Alert{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "event-hub",
			Namespace: namespace.Name,
		},
		Spec: notiv1beta1.AlertSpec{
			ProviderRef: meta.LocalObjectReference{
				Name: provider.Name,
			},
			EventSources: []notiv1beta1.CrossNamespaceObjectReference{
				{
					Kind:      "GitRepository",
					Name:      "flux-system",
					Namespace: "flux-system",
				},
			},
		},
	}
	err = kubeClient.Create(ctx, &alert)
	require.NoError(t, err)

	// Wait for message in event hub
	address := string(eventHubSecret.Data["address"])
	fmt.Println(address)
	hub, err := eventhub.NewHubFromConnectionString(address)
	require.NoError(t, err)
	runtimeInfo, err := hub.GetRuntimeInformation(ctx)
	require.NoError(t, err)
	handler := func(c context.Context, event *eventhub.Event) error {
		fmt.Println(string(event.Data))
		return nil
	}
	for _, partitionID := range runtimeInfo.PartitionIDs {
		listenerHandle, err := hub.Receive(ctx, partitionID, handler)
		require.NoError(t, err)
		listenerHandle.Close(ctx)
	}
	require.Equal(t, 1, 0)
}*/

// TODO: Enable when source-controller supports Helm charts from OCI sources.
/*func TestACRHelmRelease(t *testing.T) {
	ctx := context.TODO()

	// Create namespace for test
	namespace := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "acr-helm-release",
		},
	}
	err := kubeClient.Create(ctx, &namespace)
	require.NoError(t, err)
	defer func() {
		kubeClient.Delete(ctx, &namespace)
		require.NoError(t, err)
	}()

	// Copy ACR credentials to new namespace
	acrNn := types.NamespacedName{
		Name:      "acr-helm",
		Namespace: "flux-system",
	}
	acrSecret := corev1.Secret{}
	err = kubeClient.Get(ctx, acrNn, &acrSecret)
	require.NoError(t, err)
	acrSecret.ObjectMeta = metav1.ObjectMeta{
		Name:      acrSecret.Name,
		Namespace: namespace.Name,
	}
	err = kubeClient.Create(ctx, &acrSecret)
	require.NoError(t, err)

	// Create HelmRepository and wait for it to sync
	helmRepository := sourcev1.HelmRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "acr",
			Namespace: namespace.Name,
		},
		Spec: sourcev1.HelmRepositorySpec{
			URL: "https://acrappsoarfish.azurecr.io/helm/podinfo",
			Interval: metav1.Duration{
				Duration: 5 * time.Minute,
			},
			SecretRef: &meta.LocalObjectReference{
				Name: acrSecret.Name,
			},
			PassCredentials: true,
		},
	}
	err = kubeClient.Create(ctx, &helmRepository)
	require.NoError(t, err)
}*/
