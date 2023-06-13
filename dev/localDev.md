how to run KubeFinder locally, with a local cluster 
(e.g. kind, minikube, microk8s, etc.)
//
1. install the following tools with brew:
- k3d
- tilt

2. create a local registry with k3d
- k3d registry create 

3. create a local cluster with k3d
- k3d cluster create --registry-use k3d-registry:55488

4. from the root of the project, run tilt
- cd tilt && tilt up

5. press 'space' key to open the browser to the tilt dashboard

6. apply the secret k apply -f config/samples/kube-finder-secret.yaml

6. from now, any changes to the code will be automatically reflected in the controller


--------------------------------------------------------------------------------------
