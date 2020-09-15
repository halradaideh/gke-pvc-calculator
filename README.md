# GKE pvc Calculator

This tool was created to export PVC statistics to GCP monitor as metrics, in order to create alerts and monitor disk usage.

The tool is a go program that mount the host mount points of the pvs and check there size. it is deployed as a daemonset in all the kubernetes nodes.

----
to build :
```bash
$ docker build -t image:tag .
```

notes:
* please update the image on the deployment with the proper name and tag
* please add *GCP_PROJECT* with the name of you project to watch

----
to deploy, go to the deploy directory and apply the yaml files
```bash
$ kubectl apply -f rbac.yaml
$ kubectl apply -f gke-daemonset.yaml

```
----
todo :
* remove hack from code
* create helm deployment
* push image to docker registry
* 