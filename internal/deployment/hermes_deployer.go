package deployment

import (
	"context"
	"fmt"
	"log/slog"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// HermesDeployer manages Hermes relayer deployments in Kubernetes
type HermesDeployer struct {
	logger    *slog.Logger
	clientset kubernetes.Interface
}

// NewHermesDeployer creates a new Hermes deployer
func NewHermesDeployer(logger *slog.Logger, clientset kubernetes.Interface) *HermesDeployer {
	return &HermesDeployer{
		logger:    logger,
		clientset: clientset,
	}
}

// DeployHermes creates a Hermes deployment for relaying between provider and consumer
// Following the architecture of one Hermes per consumer chain for simplicity
func (d *HermesDeployer) DeployHermes(ctx context.Context, providerChainID, consumerChainID, namespace string, config []byte) error {
	d.logger.Info("Deploying Hermes relayer for consumer chain",
		"provider", providerChainID,
		"consumer", consumerChainID,
		"namespace", namespace)

	// Create ConfigMap with Hermes configuration
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("hermes-config-%s", consumerChainID),
			Namespace: namespace,
			Labels: map[string]string{
				"app":               "hermes",
				"consumer-chain-id": consumerChainID,
				"provider-chain-id": providerChainID,
			},
		},
		Data: map[string]string{
			"config.toml": string(config),
		},
	}

	if _, err := d.clientset.CoreV1().ConfigMaps(namespace).Create(ctx, configMap, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create Hermes config: %w", err)
	}

	// Create PVC for Hermes data
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("hermes-data-%s", consumerChainID),
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
		},
	}

	if _, err := d.clientset.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create Hermes PVC: %w", err)
	}

	// Create Deployment
	deployment := d.createHermesDeployment(providerChainID, consumerChainID, namespace)
	if _, err := d.clientset.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create Hermes deployment: %w", err)
	}

	d.logger.Info("Hermes deployment created successfully",
		"consumer", consumerChainID)

	return nil
}

// createHermesDeployment creates the Kubernetes deployment spec for Hermes
func (d *HermesDeployer) createHermesDeployment(providerChainID, consumerChainID, namespace string) *appsv1.Deployment {
	deploymentName := fmt.Sprintf("hermes-%s", consumerChainID)
	replicas := int32(1)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
			Labels: map[string]string{
				"app":               "hermes",
				"consumer-chain-id": consumerChainID,
				"provider-chain-id": providerChainID,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":               "hermes",
					"consumer-chain-id": consumerChainID,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":               "hermes",
						"consumer-chain-id": consumerChainID,
						"provider-chain-id": providerChainID,
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name:  "init-keys",
							Image: "informalsystems/hermes:1.13.1", // Official Hermes image from Docker Hub
							Command: []string{
								"sh", "-c",
								`echo "Setting up Hermes keys..."
								mkdir -p /home/hermes/.hermes/keys

								# Extract provider keys if mounted
								if [ -f "/keys/provider/keyring-archive" ]; then
								  echo "Extracting provider keys..."
								  mkdir -p /tmp/provider-keys
								  base64 -d /keys/provider/keyring-archive | tar xzf - -C /tmp/provider-keys

								  # Convert to Hermes format (placeholder - needs proper implementation)
								  echo "TODO: Convert keyring to Hermes format"
								else
								  echo "WARNING: No provider keys found"
								fi

								# For now, document manual key addition
								echo "Keys must be added manually:"
								echo "  hermes keys add --chain ` + providerChainID + ` --key-file /keys/provider.json"
								echo "  hermes keys add --chain ` + consumerChainID + ` --key-file /keys/consumer.json"`,
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "hermes-data",
									MountPath: "/home/hermes/.hermes",
								},
								{
									Name:      "provider-keys",
									MountPath: "/keys/provider",
									ReadOnly:  true,
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "hermes",
							Image: "informalsystems/hermes:1.13.1", // Official Hermes image from Docker Hub
							Command: []string{
								"hermes",
								"start",
							},
							Env: []corev1.EnvVar{
								{
									Name:  "RUST_LOG",
									Value: "info",
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "hermes-config",
									MountPath: "/home/hermes/.hermes",
									ReadOnly:  true,
								},
								{
									Name:      "hermes-data",
									MountPath: "/home/hermes/.hermes/keys",
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("256Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("512Mi"),
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"hermes",
											"health-check",
										},
									},
								},
								InitialDelaySeconds: 30,
								PeriodSeconds:       10,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"hermes",
											"health-check",
										},
									},
								},
								InitialDelaySeconds: 10,
								PeriodSeconds:       5,
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "hermes-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: fmt.Sprintf("hermes-config-%s", consumerChainID),
									},
								},
							},
						},
						{
							Name: "hermes-data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: fmt.Sprintf("hermes-data-%s", consumerChainID),
								},
							},
						},
						{
							Name: "provider-keys",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: "hermes-keys",
									Optional:   &[]bool{true}[0],
								},
							},
						},
					},
				},
			},
		},
	}
}

// DeleteHermes removes Hermes deployment for a consumer chain
func (d *HermesDeployer) DeleteHermes(ctx context.Context, consumerChainID, namespace string) error {
	d.logger.Info("Deleting Hermes deployment", "consumer", consumerChainID)

	// Delete deployment
	deploymentName := fmt.Sprintf("hermes-%s", consumerChainID)
	if err := d.clientset.AppsV1().Deployments(namespace).Delete(ctx, deploymentName, metav1.DeleteOptions{}); err != nil {
		d.logger.Warn("Failed to delete Hermes deployment", "error", err)
	}

	// Delete ConfigMap
	configMapName := fmt.Sprintf("hermes-config-%s", consumerChainID)
	if err := d.clientset.CoreV1().ConfigMaps(namespace).Delete(ctx, configMapName, metav1.DeleteOptions{}); err != nil {
		d.logger.Warn("Failed to delete Hermes config", "error", err)
	}

	// Delete PVC
	pvcName := fmt.Sprintf("hermes-data-%s", consumerChainID)
	if err := d.clientset.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, pvcName, metav1.DeleteOptions{}); err != nil {
		d.logger.Warn("Failed to delete Hermes PVC", "error", err)
	}

	return nil
}

// GetHermesStatus checks the status of Hermes deployment
func (d *HermesDeployer) GetHermesStatus(ctx context.Context, consumerChainID, namespace string) (*HermesStatus, error) {
	deploymentName := fmt.Sprintf("hermes-%s", consumerChainID)

	deployment, err := d.clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get Hermes deployment: %w", err)
	}

	return &HermesStatus{
		Ready:           deployment.Status.ReadyReplicas > 0,
		Replicas:        deployment.Status.Replicas,
		ReadyReplicas:   deployment.Status.ReadyReplicas,
		UpdatedReplicas: deployment.Status.UpdatedReplicas,
	}, nil
}

// HermesStatus represents the status of a Hermes deployment
type HermesStatus struct {
	Ready           bool
	Replicas        int32
	ReadyReplicas   int32
	UpdatedReplicas int32
}
