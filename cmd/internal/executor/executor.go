package executor

import (
	"bytes"
	"io"
	"strings"

	"github.com/metal-stack/csilvmctl/cmd/internal/helper"
	"github.com/spf13/viper"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/klog"
	"k8s.io/kubectl/pkg/scheme"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/remotecommand"
)

type Executor struct {
	podName   string
	namespace string
	node      string
	clientset *kubernetes.Clientset
	config    *restclient.Config
}

func New(clientset *kubernetes.Clientset, config *restclient.Config, node string, namespace string, podName string) *Executor {
	return &Executor{
		podName:   podName,
		namespace: namespace,
		node:      node,
		clientset: clientset,
		config:    config,
	}
}

func (e *Executor) Start() error {

	hostPathType := v1.HostPathDirectoryOrCreate
	privileged := true
	mountPropagationBidirectional := v1.MountPropagationBidirectional
	terminationGracePeriod := int64(0)
	migratorPod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      e.podName,
			Namespace: e.namespace,
		},
		Spec: v1.PodSpec{
			RestartPolicy: v1.RestartPolicyNever,
			NodeName:      e.node,
			Tolerations: []v1.Toleration{
				{
					Operator: v1.TolerationOpExists,
				},
			},
			Containers: []v1.Container{
				{
					Name:            e.podName,
					Image:           viper.GetString("migrator-pod-image"),
					ImagePullPolicy: v1.PullIfNotPresent,
					Command:         []string{"tail", "-f", "/dev/null"},
					VolumeMounts: []v1.VolumeMount{
						{
							Name:             "devices",
							ReadOnly:         false,
							MountPath:        "/dev",
							MountPropagation: &mountPropagationBidirectional,
						},
						{
							Name:      "modules",
							ReadOnly:  false,
							MountPath: "/lib/modules",
						},
						{
							Name:             "lvmbackup",
							ReadOnly:         false,
							MountPath:        "/etc/lvm/backup",
							MountPropagation: &mountPropagationBidirectional,
						},
						{
							Name:             "lvmcache",
							ReadOnly:         false,
							MountPath:        "/etc/lvm/cache",
							MountPropagation: &mountPropagationBidirectional,
						},
						{
							Name:             "tmpcsi",
							ReadOnly:         false,
							MountPath:        "/tmp/csi-lvm",
							MountPropagation: &mountPropagationBidirectional,
						},
					},
					TerminationMessagePath: "/termination.log",
					SecurityContext: &v1.SecurityContext{
						Privileged: &privileged,
					},
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							"cpu":    resource.MustParse("50m"),
							"memory": resource.MustParse("50Mi"),
						},
						Limits: v1.ResourceList{
							"cpu":    resource.MustParse("100m"),
							"memory": resource.MustParse("100Mi"),
						},
					},
				},
			},
			TerminationGracePeriodSeconds: &terminationGracePeriod,
			Volumes: []v1.Volume{
				{
					Name: "devices",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: "/dev",
							Type: &hostPathType,
						},
					},
				},
				{
					Name: "modules",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: "/lib/modules",
							Type: &hostPathType,
						},
					},
				},
				{
					Name: "lvmbackup",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: "/etc/lvm/backup",
							Type: &hostPathType,
						},
					},
				},
				{
					Name: "lvmcache",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: "/etc/lvm/cache",
							Type: &hostPathType,
						},
					},
				},
				{
					Name: "lvmlock",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: "/run/lock/lvm",
							Type: &hostPathType,
						},
					},
				},
				{
					Name: "tmpcsi",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: "/tmp/csi-lvm",
							Type: &hostPathType,
						},
					},
				},
			},
		},
	}

	err := helper.StartPodAndWait(e.clientset, e.namespace, migratorPod)
	if err != nil {
		return err
	}

	return nil
}

func (e *Executor) Exec(command string, stdin io.Reader) (string, string, error) {

	var stdout, stderr bytes.Buffer

	cmd := []string{
		"sh",
		"-c",
		command,
	}
	req := e.clientset.CoreV1().RESTClient().Post().Resource("pods").Name(e.podName).Namespace(e.namespace).SubResource("exec")
	option := &v1.PodExecOptions{
		Command: cmd,
		Stdin:   true,
		Stdout:  true,
		Stderr:  true,
		TTY:     true,
	}
	if stdin == nil {
		option.Stdin = false
	}
	req.VersionedParams(
		option,
		scheme.ParameterCodec,
	)
	exec, err := remotecommand.NewSPDYExecutor(e.config, "POST", req.URL())
	if err != nil {
		return "", "", err
	}
	err = exec.Stream(remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), nil
}

func (e *Executor) Destroy() {
	err := helper.DestroyPodAndWait(e.clientset, e.namespace, e.podName)
	if err != nil {
		klog.Errorf("unable to delete the migrator pod: %v", err)
	}
}
