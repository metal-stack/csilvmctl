
# csi-driver-lvm command line utility

***BETA - use at own risk***

Can currently be used to migrate a csi-lvm PersistentVolumeClaim to csi-driver-lvm.
Make sure to have a recent backup (velero) before using.

## Usage

```bash
Usage:
  csilvmctl [command]

Aliases:
  csilvmctl, m

Available Commands:
  help        Help about any command
  migrate     migrate a csi-lvm PersistentVolumeClaim to csi-driver-lvm

Flags:
  -h, --help                        help for csilvmctl
      --kubeconfig string           Path to the kube-config to use for authentication and authorization. Is updated by login. (default "~/.kube/config")
      --migrator-pod-image string   image used for the migratior pod (default "metalstack/lvmplugin:v0.3.5")
  -n, --namespace string            namespace
      --provisioner string          csi-driver-lvm storage provisioner (default "lvm.csi.metal-stack.io")
      --vgname string               name of the lvm volume group (default "csi-lvm")
  -y, --yes                         answer yes to all questions
```

## Example

```bash
$ kubectl get pvc
NAME              STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS   AGE
storage-my-db-0   Bound    pvc-7198a307-2c66-421c-9cec-f545a445d5d2   50Gi       RWO            csi-lvm        9d

$ csilvmctl migrate storage-my-db-0
Error: error: pvc storage-my-db-0 is in use by pod my-db-0

$ kubectl scale statefulset my-db --replicas=0
statefulset.apps/my-db scaled

$ csilvmctl migrate storage-my-db-0
Migrating volume storage-my-db-0 (pvc-7198a307-2c66-421c-9cec-f545a445d5d2) on node shoot--pz9cjf--mwen-stg-default-worker-5cd4d79b49-jlmnl to new storage class csi-lvm-sc-mirror
Do you want to proceed? (y/n) y
Please wait ...
Volume storage-my-db-0 successfully migrated to csi-driver-lvm. You can start your pod again.
Make sure to also change the storage class in your source files to the new storageClassName csi-lvm-sc-mirror.

$ kubectl scale statefulset my-db --replicas=1
statefulset.apps/my-db scaled

$ kubectl get pvc
NAME              STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS        AGE
storage-my-db-0   Bound    pvc-15e29a14-bf9b-4107-8a5f-a4721899ff9f   50Gi       RWO            csi-lvm-sc-mirror   60s
```
