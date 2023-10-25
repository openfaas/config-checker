#!/bin/bash

mkdir -p ./openfaas

echo "Gathering diagnostics to: ./openfaas"

kubectl get -n openfaas deploy -o wide > openfaas/openfaas-core-deploy.txt
kubectl get -n openfaas configmap -o yaml > openfaas/openfaas-configmaps.yaml
kubectl get -n openfaas-fn function -o yaml > openfaas/openfaas-function-crd.yaml
kubectl get -n openfaas deploy -o yaml > openfaas/openfaas-deploy.yaml
kubectl get -n openfaas-fn deploy -o yaml > openfaas/openfaas-fn-deploy.yaml
kubectl logs -n openfaas deploy/gateway -c operator > openfaas/operator-logs.txt
kubectl logs -n openfaas deploy/gateway -c gateway > openfaas/gateway-logs.txt
kubectl get events -n openfaas --sort-by=.metadata.creationTimestamp > openfaas/openfaas-events.txt
kubectl get events -n openfaas-fn --sort-by=.metadata.creationTimestamp > openfaas/openfaas-fn-events.txt
kubectl get clusterrole > openfaas/clusterrole-list.txt
kubectl get clusterrole -o yaml > openfaas/clusterrole.yaml

echo ""

NAME=openfaas-$(date '+%Y-%m-%d_%H_%M_%S').tgz

echo Creating $NAME...

tar -czvf ./$NAME openfaas/
