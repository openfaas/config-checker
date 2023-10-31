#!/bin/bash

mkdir -p ./openfaas

echo "Gathering diagnostics to: ./openfaas"

# Core service deployment list, and images
kubectl get -n openfaas deploy -o wide > openfaas/openfaas-core-deploy.txt

# ConfigMap and function CRD YAML
kubectl get -n openfaas configmap -o yaml > openfaas/openfaas-configmaps.yaml
kubectl get -n openfaas-fn function -o yaml > openfaas/openfaas-function-crd.yaml

# Function and core service YAML
kubectl get -n openfaas deploy -o yaml > openfaas/openfaas-deploy.yaml
kubectl get -n openfaas-fn deploy -o yaml > openfaas/openfaas-fn-deploy.yaml

# Logs from core services
kubectl logs -n openfaas deploy/gateway -c operator > openfaas/operator-logs.txt
kubectl logs -n openfaas deploy/gateway -c gateway > openfaas/gateway-logs.txt

# Events by order from core services
kubectl get events -n openfaas --sort-by=.metadata.creationTimestamp > openfaas/openfaas-events.txt
kubectl get events -n openfaas-fn --sort-by=.metadata.creationTimestamp > openfaas/openfaas-fn-events.txt

# RBAC 
kubectl get role -n openfaas > openfaas/role-list.txt
kubectl get role -n openfaas -o yaml > openfaas/role.yaml

kubectl get clusterrole > openfaas/clusterrole-list.txt
kubectl get clusterrole -o yaml > openfaas/clusterrole.yaml

kubectl get serviceaccount -n openfaas > openfaas/serviceaccount-list.txt
kubectl get serviceaccount -n openfaas -o yaml > openfaas/serviceaccount.yaml

echo ""

NAME=openfaas-$(date '+%Y-%m-%d_%H_%M_%S').tgz

echo Creating $NAME...

tar -czvf ./$NAME openfaas/
