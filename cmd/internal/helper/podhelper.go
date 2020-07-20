package helper

import (
	"context"
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"

	"k8s.io/client-go/kubernetes"
)

func StartPodAndWait(clientset *kubernetes.Clientset, namespace string, pod *v1.Pod) error {
	_, err := clientset.CoreV1().Pods(namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	retrySeconds := 60
	for i := 0; i < retrySeconds; i++ {
		pod, err := clientset.CoreV1().Pods(namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
		if pod != nil && pod.Status.Phase == v1.PodFailed {
			// pod terminated in time, but with failure
			klog.Info("executor pod terminated with failure")
			return err
		}
		if err != nil {
			klog.Errorf("error reading executor pod:%v", err)
		} else if pod.Status.Phase == v1.PodRunning {
			// klog.Info(" pod started successfully")
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("pod creation timeout after %v seconds", retrySeconds)
}

func DestroyPodAndWait(clientset *kubernetes.Clientset, namespace string, podName string) error {
	err := clientset.CoreV1().Pods(namespace).Delete(context.TODO(), podName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("unable to delete the migrator pod: %v", err)
	}
	retrySeconds := 60
	for i := 0; i < retrySeconds; i++ {
		pod, err := clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
		if pod != nil && pod.ObjectMeta.Name != podName {
			return nil
		}
		if err != nil {
			return fmt.Errorf("error reading provisioner pod %v: %v", pod, err)
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("unable to delete the migrator pod: %v", err)
}
