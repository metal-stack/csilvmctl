package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/metal-stack/csilvmctl/cmd/internal/executor"
	"github.com/metal-stack/csilvmctl/cmd/internal/helper"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	// needed for kubectl auth
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

var (
	migrateCmd = &cobra.Command{
		Use:   "migrate",
		Short: "migrate a csi-lvm PersistentVolumeClaim to csi-driver-lvm",
		Long:  "migrate a csi-lvm PersistentVolumeClaim to csi-driver-lvm",
		RunE: func(cmd *cobra.Command, args []string) error {
			return migrateVolume(args)
		},
	}
)

func init() {
	viper.BindPFlags(migrateCmd.Flags())
}

func migrateVolume(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no pvc given")
	}
	pvcName := args[0]

	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", viper.GetString("kubeconfig"))
	if err != nil {
		panic(err.Error())
	}
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
	namespace, _, _ := kubeConfig.Namespace()
	if viper.GetString("namespace") != "" {
		namespace = viper.GetString("namespace")
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	// get existing csi-driver-lvm storage classes
	storageClasses := make(map[string]string)
	scs, err := clientset.StorageV1().StorageClasses().List(context.TODO(), metav1.ListOptions{})
	for _, s := range scs.Items {
		if s.Provisioner == viper.GetString("provisioner") {
			storageClasses[s.Parameters["type"]] = s.Name
		}
	}

	// get pvc api object
	pvc, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Get(context.TODO(), pvcName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	oldVolumeName := pvc.Spec.VolumeName
	oldVolume, err := clientset.CoreV1().PersistentVolumes().Get(context.TODO(), oldVolumeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("old volume %s not found: %s", oldVolumeName, err)
	}

	// get size of volume
	s, _ := pvc.Spec.Resources.Requests.Storage().AsInt64()
	originalSize := resource.NewQuantity(s, resource.BinarySI).String()

	// get node where the volume is located
	node := oldVolume.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Values[0]
	if err != nil {
		klog.Fatal(err)
	}

	// start our migrator pod on that volume
	migratorPod := executor.New(clientset, config, node, namespace, "csi-lvm-migrator-pod-"+pvcName)
	migratorPod.Start()
	if err != nil {
		return err
	}
	// make sure the pod gets removed after we're done
	defer func() {
		migratorPod.Destroy()
	}()

	// check if volume group exists
	vgname := viper.GetString("vgname")
	stdout, stderr, err := migratorPod.Exec("vgs --no-headings -o vg_name "+vgname, nil)
	if err != nil {
		return err
	}
	if stdout != vgname {
		return fmt.Errorf("volume group %s not found: %s %s", vgname, stdout, stderr)
	}

	// check if volume is an csi-lvm volume
	stdout, stderr, err = migratorPod.Exec("lvs --no-headings -o lv_tags "+vgname+"/"+oldVolumeName, nil)
	if err != nil {
		return err
	}
	if !strings.Contains(stdout, "lv.metal-stack.io/csi-lvm") {
		return fmt.Errorf("volume %s is not of type csi-lvm (does not contain tag \"lv.metal-stack.io/csi-lvm\"", oldVolumeName)
	}

	// get lvmType of the volume
	lvmType, stderr, err := migratorPod.Exec("lvs --no-headings -o lv_layout "+vgname+"/"+oldVolumeName, nil)
	if err != nil {
		return err
	}
	if lvmType == "raid,raid1" {
		lvmType = "mirror"
	}

	// find new storage class
	newStorageClass := storageClasses[lvmType]
	if newStorageClass == "" {
		return fmt.Errorf("no matching csi-driver-lvm storage class found for type %s %s", lvmType, stderr)
	}

	// check for running pods
	pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, p := range pods.Items {
		for _, v := range p.Spec.Volumes {
			if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == pvcName {
				return fmt.Errorf("error: pvc %s is in use by pod %s", pvcName, p.GetName())
			}
		}
	}

	fmt.Printf("Migrating volume %s (%s) on node %s to new storage class %s\n", pvc.GetName(), oldVolumeName, node, newStorageClass)
	if !viper.GetBool("yes") {
		if err := helper.Prompt("Do you want to proceed? (y/n) ", "y"); err != nil {
			return err
		}
	}
	fmt.Println("Please wait ...")

	// set volume to retain
	err = setVolumeToRetain(clientset, oldVolumeName)
	if err != nil {
		return err
	}

	// delete old pvc
	err = clientset.CoreV1().PersistentVolumeClaims(namespace).Delete(context.TODO(), pvcName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("cannot remove pvc %s: %s", pvcName, err)
	}

	// wait till pvc is gone
	retrySeconds := 60
	for i := 0; i < retrySeconds; i++ {
		tp, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Get(context.TODO(), pvcName, metav1.GetOptions{})
		if tp != nil && tp.ObjectMeta.Name != pvcName {
			break
		}
		if err != nil {
			return fmt.Errorf("error getting pvc %v: %v", pvcName, err)
		}
		time.Sleep(1 * time.Second)
	}

	// create new pvc
	_, err = clientset.CoreV1().PersistentVolumeClaims(namespace).Create(context.TODO(), &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvcName,
		},
		Spec: v1.PersistentVolumeClaimSpec{
			StorageClassName: &newStorageClass,
			VolumeMode:       pvc.Spec.VolumeMode,
			AccessModes: []v1.PersistentVolumeAccessMode{
				v1.ReadWriteOnce,
			},
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceName(v1.ResourceStorage): resource.MustParse("1Mi"),
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("could not create the new pvc: %s", err)
	}

	// create dummy pod so that the lvm volume gets actually crated on the target node
	tempMountPodName := "temp-mountpod-" + pvcName
	err = startMounterPod(clientset, node, namespace, tempMountPodName, pvcName)
	if err != nil {
		return err
	}

	// get new pv name
	pvc, err = clientset.CoreV1().PersistentVolumeClaims(namespace).Get(context.TODO(), pvcName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	newVolumeName := pvc.Spec.VolumeName

	// delete dummy pod
	err = helper.DestroyPodAndWait(clientset, namespace, tempMountPodName)
	if err != nil {
		return err
	}

	// move volume
	// umount /tmp/oldVolume
	// lvremove -y newVolume
	// lvrename oldVolume newVolume
	// lvchange --deltag lv.metal-stack.io/csi-lvm newVolume (was oldVolume)
	// lvchange --addtag vg.metal-stack.io/csi-lvm-driver newVolume (was oldVolume)

	stdout, stderr, err = migratorPod.Exec("umount /tmp/csi-lvm/"+oldVolumeName, nil)
	if err != nil {
		return fmt.Errorf("unable to umount volume %s: %s %s %s", oldVolumeName, err, stdout, stderr)
	}
	stdout, stderr, err = migratorPod.Exec("lvremove -y "+vgname+"/"+newVolumeName, nil)
	if err != nil {
		return fmt.Errorf("unable to remove dummy volume %s: %s %s %s", newVolumeName, err, stdout, stderr)
	}
	stdout, stderr, err = migratorPod.Exec("lvrename "+vgname+"/"+oldVolumeName+" "+vgname+"/"+newVolumeName, nil)
	if err != nil {
		return fmt.Errorf("unable to rename volume %s to %s: %s %s %s", oldVolumeName, newVolumeName, err, stdout, stderr)
	}
	stdout, stderr, err = migratorPod.Exec("lvchange --deltag lv.metal-stack.io/csi-lvm "+vgname+"/"+newVolumeName, nil)
	if err != nil {
		return fmt.Errorf("unable to remove tag lv.metal-stack.io/csi-lvm from %s: %s %s %s", newVolumeName, err, stdout, stderr)
	}
	stdout, stderr, err = migratorPod.Exec("lvchange --addtag vg.metal-stack.io/csi-lvm-driver  "+vgname+"/"+newVolumeName, nil)
	if err != nil {
		return fmt.Errorf("unable to add tag vg.metal-stack.io/csi-lvm-driver  from %s: %s %s %s", newVolumeName, err, stdout, stderr)
	}

	err = clientset.CoreV1().PersistentVolumes().Delete(context.TODO(), oldVolumeName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("unable remove old pventry %s: %s", oldVolumeName, err)
	}

	// resize volumeclaim (volume itself already has the correct size), mount again to enforce resize
	err = updateVolumeSize(clientset, namespace, pvcName, originalSize)
	if err != nil {
		return err
	}

	fmt.Printf("Volume %s successfully migrated to csi-driver-lvm. You can start your pod again.\n", pvc.GetName())
	fmt.Printf("Make sure to also change the storage class in your source files to the new storageClassName %s.\n", newStorageClass)
	return nil
}

func setVolumeToRetain(clientset *kubernetes.Clientset, volumeName string) error {
	vols := clientset.CoreV1().PersistentVolumes()
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Assumes you've already deployed redis before to the cluster
		result, err := vols.Get(context.TODO(), volumeName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("Failed to get latest volumes: %s", err)
		}
		result.Spec.PersistentVolumeReclaimPolicy = v1.PersistentVolumeReclaimRetain
		_, err = vols.Update(context.TODO(), result, metav1.UpdateOptions{})
		return err
	})
	if retryErr != nil {
		return retryErr
	}
	return nil
}

func updateVolumeSize(clientset *kubernetes.Clientset, namespace string, pvcName string, size string) error {
	pvcs := clientset.CoreV1().PersistentVolumeClaims(namespace)
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Assumes you've already deployed redis before to the cluster
		result, err := pvcs.Get(context.TODO(), pvcName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("Failed to get latest volumeclaims: %s", err)
		}
		result.Spec.Resources.Requests = v1.ResourceList{
			v1.ResourceName(v1.ResourceStorage): resource.MustParse(size),
		}
		_, err = pvcs.Update(context.TODO(), result, metav1.UpdateOptions{})
		return err
	})
	if retryErr != nil {
		return retryErr
	}
	return nil
}

func startMounterPod(clientset *kubernetes.Clientset, node string, namespace string, name string, pvcName string) error {

	terminationGracePeriod := int64(0)
	tempMountPod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1.PodSpec{
			RestartPolicy: v1.RestartPolicyNever,
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": node,
			},
			TerminationGracePeriodSeconds: &terminationGracePeriod,
			Containers: []v1.Container{
				{
					Name:            "pvc",
					Image:           viper.GetString("migrator-pod-image"),
					ImagePullPolicy: v1.PullIfNotPresent,
					Command:         []string{"sleep", "60"},
				},
			},
			Volumes: []v1.Volume{
				{
					Name: name,
					VolumeSource: v1.VolumeSource{
						PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
		},
	}
	err := helper.StartPodAndWait(clientset, namespace, tempMountPod)
	if err != nil {
		return fmt.Errorf("could not create mount pod: %s", err)
	}
	return nil
}
