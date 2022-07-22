# Installation

## Installing with Helm

cloudflare-operator provides a Helm chart as a first-class method of installation on Kubernetes.

### Prerequisites

- Install Helm version 3 or later
- Install a supported version of Kubernetes

### Steps

1. Add the containeroo Helm repository:

This repository is the only supported source of containeroo charts.

```bash
helm repo add containeroo https://charts.containeroo.ch
```

2. Update your local Helm chart repository cache:

```bash
helm repo update
```

3. Install CustomResourceDefinitions

cloudflare-operator requires a number of CRD resources, which must be installed manually using `kubectl`.

Installing CRDs with kubectl:

```bash
kubectl apply -f https://github.com/containeroo/cloudflare-operator/releases/download/v0.3.2/crds.yaml
```

4. Install cloudflare-operator

To install the cloudflare-operator Helm chart, use the Helm install command as described below.

```bash
helm install \
  cloudflare-operator containeroo/cloudflare-operator \
  --namespace cloudflare-operator \
  --create-namespace \
  --version v0.3.2
```

A full list of available Helm values is on [cloudflare-operator’s ArtifactHub page](https://artifacthub.io/packages/helm/containeroo/cloudflare-operator).

## Output YAML

Instead of directly installing cloudflare-operator using Helm, a static YAML manifest can be generated using the Helm template command. This static manifest can be tuned by providing the flags to overwrite the default Helm values:

```bash
helm template \
  cloudflare-operator containeroo/cloudflare-operator \
  --namespace cloudflare-operator \
  --create-namespace \
  --version v0.3.2 \
  --set your.value=here
```

## Uninstalling

!!! warning
    To uninstall cloudflare-operator you should always use the same process for installing but in reverse. Deviating from the following process whether cloudflare-operator has been installed from static manifests or Helm can cause issues and potentially broken states. Please ensure you follow the below steps when uninstalling to prevent this happening.

Before continuing, ensure that all cloudflare-operator resources that have been created by users have been deleted. You can check for any existing resources with the following command:

```bash
kubectl get Account,DNSRecord,IP --all-namespaces
```

Once all these resources have been deleted you are ready to uninstall cloudflare-operator using the procedure determined by how you installed.

### Uninstalling with Helm

Uninstalling cloudflare-operator from a helm installation is a case of running the installation process, in reverse, using the delete command on both `kubectl` and `helm`.

```bash
helm --namespace cloudflare-operator delete cloudflare-operator
```

```bash
kubectl delete namespace cloudflare-operator
```

Finally, delete the cloudflare-operator CustomResourceDefinitions using the link to the version vX.Y.Z you installed:

!!! warning
    This command will also remove installed cloudflare-operator objects. All cloudflare-operator resources will be removed by Kubernetes' garbage collector.

```bash
kubectl delete -f https://github.com/containeroo/cloudflare-operator/releases/download/v0.3.2/crds.yaml
```

## Namespace Stuck in Terminating State

If the namespace has been marked for deletion without deleting the cloudflare-operator installation first, the namespace may become stuck in a terminating state. This is typically due to the fact that the APIService resource still exists however the webhook is no longer running so is no longer reachable. To resolve this, ensure you have run the above commands correctly, and if you’re still experiencing issues then run:

```bash
kubectl delete apiservice v1beta1.cf.containeroo.ch
```
