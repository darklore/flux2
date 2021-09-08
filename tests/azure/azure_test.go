package test

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	//eventhub "github.com/Azure/azure-event-hubs-go/v3"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/hashicorp/terraform-exec/tfinstall"
	git2go "github.com/libgit2/git2go/v31"
	"github.com/microsoft/azure-devops-go-api/azuredevops"
	"github.com/microsoft/azure-devops-go-api/azuredevops/git"
	"github.com/stretchr/testify/require"
	giturls "github.com/whilp/git-urls"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	notiv1beta1 "github.com/fluxcd/notification-controller/api/v1beta1"
	//helmv2beta1 "github.com/fluxcd/helm-controller/api/v2beta1"
	reflectorv1beta1 "github.com/fluxcd/image-reflector-controller/api/v1beta1"
	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
)

type config struct {
	kubeconfigPath string
	kubeClient     client.Client

	azdoPat                  string
	sharedSopsId             string
	acrUrl                   string
	fleetInfraRepositoryUrl  string
	applicationRepositoryUrl string
}

var cfg config

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
	sharedSopsId := outputs["shared_sops_id"].Value.(string)
	acrUrl := outputs["acr_url"].Value.(string)
	kubeconfig := outputs["aks_kube_config"].Value.(string)
	aksHost := outputs["aks_host"].Value.(string)
	aksCert := outputs["aks_client_certificate"].Value.(string)
	aksKey := outputs["aks_client_key"].Value.(string)
	aksCa := outputs["aks_cluster_ca_certificate"].Value.(string)
	fleetInfraRepositoryUrl := outputs["fleet_infra_repository_url"].Value.(string)
	applicationRepositoryUrl := outputs["application_repository_url"].Value.(string)

	log.Println("Creating Kubernetes client")
	kubeconfigPath, kubeClient, err := getKubernetesCredentials(kubeconfig, aksHost, aksCert, aksKey, aksCa)
	if err != nil {
		log.Fatalf("error create Kubernetes client: %v", err)
	}
	defer os.RemoveAll(filepath.Dir(kubeconfigPath))
	err = installFlux(ctx, kubeClient, kubeconfigPath, fleetInfraRepositoryUrl, idRsa, idRsaPub, azdoPat)
	if err != nil {
		log.Fatalf("error installing Flux: %v", err)
	}

	cfg = config{
		kubeconfigPath:           kubeconfigPath,
		kubeClient:               kubeClient,
		azdoPat:                  azdoPat,
		sharedSopsId:             sharedSopsId,
		acrUrl:                   acrUrl,
		fleetInfraRepositoryUrl:  fleetInfraRepositoryUrl,
		applicationRepositoryUrl: applicationRepositoryUrl,
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
		err := verifyGitAndKustomization(ctx, cfg.kubeClient, "flux-system", "flux-system")
		if err != nil {
			return false
		}
		return true
	}, 30*time.Second, 1*time.Second)
}

func TestAzureDevOpsCloning(t *testing.T) {
	ctx := context.TODO()
	branchName := "feature/branch"

	tests := []struct {
		name      string
		secretRef string
		branch    string
		tag       string
	}{
		{
			name:      "https-feature-branch",
			secretRef: "https-credentials",
			branch:    branchName,
		},
		{
			name:      "https-v1",
			secretRef: "https-credentials",
			tag:       "v1",
		},
		{
			name:      "ssh-feature-branch",
			secretRef: "flux-system",
			branch:    branchName,
		},
		{
			name:      "ssh-v1",
			secretRef: "flux-system",
			tag:       "v1",
		},
	}

	t.Log("Creating application sources")
	repoUrl := cfg.applicationRepositoryUrl
	repo, repoDir, err := getRepository(repoUrl, branchName, cfg.azdoPat)
	require.NoError(t, err)
	for _, tt := range tests {
		err = runCommand(ctx, repoDir, fmt.Sprintf("mkdir -p ./cloning-test/%s", tt.name))
		require.NoError(t, err)
		err = runCommand(ctx, repoDir, fmt.Sprintf("echo '%s' > ./cloning-test/%s/configmap.yaml", getTestManifest(tt.name), tt.name))
		require.NoError(t, err)
	}
	// TODO: Need to create a tag
	//err = runCommand(ctx, repoDir, "if [ \"$(git status --porcelain)\" ]; then git add -A && git commit -m 'add application test' && git tag -d v1 && git tag v1; fi;")
	err = commitAndPushAll(repo, branchName, cfg.azdoPat)
	require.NoError(t, err)

	t.Log("Verifying application-gitops namespaces")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &sourcev1.GitRepository{ObjectMeta: metav1.ObjectMeta{Name: tt.name, Namespace: "flux-system"}}
			_, err := controllerutil.CreateOrUpdate(ctx, cfg.kubeClient, source, func() error {
				source.Spec = sourcev1.GitRepositorySpec{
					GitImplementation: sourcev1.LibGit2Implementation,
					Reference: &sourcev1.GitRepositoryRef{
						Branch: tt.branch,
						Tag:    tt.tag,
					},
					SecretRef: &meta.LocalObjectReference{
						Name: tt.secretRef,
					},
					URL: repoUrl,
				}
				return nil
			})
			require.NoError(t, err)
			kustomization := &kustomizev1.Kustomization{ObjectMeta: metav1.ObjectMeta{Name: tt.name, Namespace: "flux-system"}}
			_, err = controllerutil.CreateOrUpdate(ctx, cfg.kubeClient, kustomization, func() error {
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
				err := verifyGitAndKustomization(ctx, cfg.kubeClient, namespace, tt.name)
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
	err := cfg.kubeClient.Create(ctx, &namespace)
	require.NoError(t, err)
	defer func() {
		cfg.kubeClient.Delete(ctx, &namespace)
		require.NoError(t, err)
	}()

	// Copy ACR credentials to new namespace
	acrNn := types.NamespacedName{
		Name:      "acr-docker",
		Namespace: "flux-system",
	}
	acrSecret := corev1.Secret{}
	err = cfg.kubeClient.Get(ctx, acrNn, &acrSecret)
	require.NoError(t, err)
	acrSecret.ObjectMeta = metav1.ObjectMeta{
		Name:      acrNn.Name,
		Namespace: namespace.Name,
	}
	err = cfg.kubeClient.Create(ctx, &acrSecret)
	require.NoError(t, err)

	// Create image repository
	imageRepository := reflectorv1beta1.ImageRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "podinfo",
			Namespace: namespace.Name,
		},
		Spec: reflectorv1beta1.ImageRepositorySpec{
			Image: fmt.Sprintf("%s/container/podinfo", cfg.acrUrl),
			Interval: metav1.Duration{
				Duration: 1 * time.Minute,
			},
			SecretRef: &meta.LocalObjectReference{
				Name: acrSecret.Name,
			},
		},
	}
	err = cfg.kubeClient.Create(ctx, &imageRepository)
	require.NoError(t, err)

	// Wait for image repository to be ready
	require.Eventually(t, func() bool {
		nn := types.NamespacedName{
			Name:      imageRepository.Name,
			Namespace: imageRepository.Namespace,
		}
		checkIr := reflectorv1beta1.ImageRepository{}
		err := cfg.kubeClient.Get(ctx, nn, &checkIr)
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
	ctx := context.TODO()
	repoUrl := cfg.applicationRepositoryUrl
	branchName := "test/keyvault-sops"

	repo, tmpDir, err := getRepository(repoUrl, branchName, cfg.azdoPat)
	secretYaml := `apiVersion: v1
kind: Secret
metadata:
  name: "test"
  namespace: "key-vault-sops"
stringData:
  foo: "bar"`
	err = runCommand(ctx, tmpDir, "mkdir -p ./key-vault-sops")
	require.NoError(t, err)
	err = runCommand(ctx, tmpDir, fmt.Sprintf("echo \"%s\" > ./key-vault-sops/secret.enc.yaml", secretYaml))
	err = runCommand(ctx, tmpDir, fmt.Sprintf("sops --encrypt --azure-kv %s --in-place ./key-vault-sops/secret.enc.yaml", cfg.sharedSopsId))
	require.NoError(t, err)

	err = commitAndPushAll(repo, branchName, cfg.azdoPat)
	require.NoError(t, err)

	// Create kustomization for sops
	namespace := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "key-vault-sops",
		},
	}
	err = cfg.kubeClient.Create(ctx, &namespace)
	require.NoError(t, err)
	/*defer func() {
		cfg.kubeClient.Delete(ctx, &namespace)
		require.NoError(t, err)
	}()*/
	source := &sourcev1.GitRepository{ObjectMeta: metav1.ObjectMeta{Name: "key-vault-sops", Namespace: "flux-system"}}
	_, err = controllerutil.CreateOrUpdate(ctx, cfg.kubeClient, source, func() error {
		source.Spec = sourcev1.GitRepositorySpec{
			GitImplementation: sourcev1.LibGit2Implementation,
			Reference: &sourcev1.GitRepositoryRef{
				Branch: branchName,
			},
			SecretRef: &meta.LocalObjectReference{
				Name: "https-credentials",
			},
			URL: repoUrl,
		}
		return nil
	})
	require.NoError(t, err)
	kustomization := &kustomizev1.Kustomization{ObjectMeta: metav1.ObjectMeta{Name: "key-vault-sops", Namespace: "flux-system"}}
	_, err = controllerutil.CreateOrUpdate(ctx, cfg.kubeClient, kustomization, func() error {
		kustomization.Spec = kustomizev1.KustomizationSpec{
			Path: "./key-vault-sops",
			//TargetNamespace: namespace.Name,
			SourceRef: kustomizev1.CrossNamespaceSourceReference{
				Kind:      sourcev1.GitRepositoryKind,
				Name:      source.Name,
				Namespace: source.Namespace,
			},
			Interval: metav1.Duration{Duration: 1 * time.Minute},
			Prune:    true,
			Decryption: &kustomizev1.Decryption{
				Provider: "sops",
			},
		}
		return nil
	})
	require.NoError(t, err)
}

func TestAzureDevOpsCommitStatus(t *testing.T) {
	ctx := context.TODO()
	name := "commit-status"
	repoUrl := cfg.applicationRepositoryUrl

	repo, repoDir, err := getRepository(repoUrl, name, cfg.azdoPat)
	require.NoError(t, err)
	manifest := fmt.Sprintf(`
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: foobar
      namespace: %s
  `, name)
	err = addFile(repoDir, "configmap.yaml", manifest)
	require.NoError(t, err)
	err = commitAndPushAll(repo, name, cfg.azdoPat)
	require.NoError(t, err)

	err = setupNamespace(ctx, cfg.kubeClient, repoUrl, cfg.azdoPat, name)
	require.NoError(t, err)
	kustomization := &kustomizev1.Kustomization{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: name}}
	_, err = controllerutil.CreateOrUpdate(ctx, cfg.kubeClient, kustomization, func() error {
		kustomization.Spec.HealthChecks = []meta.NamespacedObjectKindReference{
			{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Name:       "foobar",
				Namespace:  name,
			},
		}
		return nil
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		err := verifyGitAndKustomization(ctx, cfg.kubeClient, name, name)
		if err != nil {
			return false
		}
		return true
	}, 10*time.Second, 1*time.Second)

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azuredevops-token",
			Namespace: name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, cfg.kubeClient, &secret, func() error {
		secret.StringData = map[string]string{
			"token": cfg.azdoPat,
		}
		return nil
	})
	provider := notiv1beta1.Provider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azuredevops",
			Namespace: name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, cfg.kubeClient, &provider, func() error {
		provider.Spec = notiv1beta1.ProviderSpec{
			Type:    "azuredevops",
			Address: repoUrl,
			SecretRef: &meta.LocalObjectReference{
				Name: "azuredevops-token",
			},
		}
		return nil
	})
	require.NoError(t, err)
	alert := notiv1beta1.Alert{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azuredevops",
			Namespace: name,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, cfg.kubeClient, &alert, func() error {
		alert.Spec = notiv1beta1.AlertSpec{
			ProviderRef: meta.LocalObjectReference{
				Name: provider.Name,
			},
			EventSources: []notiv1beta1.CrossNamespaceObjectReference{
				{
					Kind:      "Kustomization",
					Name:      name,
					Namespace: name,
				},
			},
		}
		return nil
	})
	require.NoError(t, err)

	u, err := giturls.Parse(repoUrl)
	require.NoError(t, err)
	id := strings.TrimLeft(u.Path, "/")
	id = strings.TrimSuffix(id, ".git")
	comp := strings.Split(id, "/")
	orgUrl := fmt.Sprintf("%s://%s/%v", u.Scheme, u.Host, comp[0])
	project := comp[1]
	repoId := comp[3]
	branch, err := repo.LookupBranch(name, git2go.BranchLocal)
	require.NoError(t, err)
	commit, err := repo.LookupCommit(branch.Target())
	rev := commit.Id().String()
	fmt.Println(rev)
	connection := azuredevops.NewPatConnection(orgUrl, cfg.azdoPat)
	client, err := git.NewClient(ctx, connection)
	require.NoError(t, err)
	getArgs := git.GetStatusesArgs{
		Project:      &project,
		RepositoryId: &repoId,
		CommitId:     &rev,
	}
	require.Eventually(t, func() bool {
		statuses, err := client.GetStatuses(ctx, getArgs)
		if err != nil {
			return false
		}
		fmt.Println(*statuses)
		if len(*statuses) != 1 {
			return false
		}
		return true
	}, 120*time.Second, 5*time.Second)
}

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
