#!/bin/bash

mkdir -p ./openfaas

echo "Gathering diagnostics to: ./openfaas"

# Core service deployment list, and images
kubectl get -n openfaas deploy -o wide > openfaas/01-openfaas-core-deploy.txt

# ConfigMap and function CRD YAML
kubectl get -n openfaas configmap -o yaml > openfaas/02-openfaas-configmaps.yaml
kubectl get -n openfaas-fn function -o yaml > openfaas/03-openfaas-function-crd.yaml

# Function and core service YAML
kubectl get -n openfaas deploy -o yaml > openfaas/04-openfaas-deploy.yaml
kubectl get -n openfaas-fn deploy -o yaml > openfaas/05-openfaas-fn-deploy.yaml

# Logs from core services
kubectl logs -n openfaas deploy/gateway -c operator > openfaas/06-operator-logs.txt
kubectl logs -n openfaas deploy/gateway -c gateway > openfaas/07-gateway-logs.txt

# Events by order from core services
kubectl get events -n openfaas --sort-by=.metadata.creationTimestamp > openfaas/08-openfaas-events.txt
kubectl get events -n openfaas-fn --sort-by=.metadata.creationTimestamp > openfaas/09-openfaas-fn-events.txt

# RBAC 
kubectl get role -n openfaas > openfaas/10-role-list.txt
kubectl get role -n openfaas -o yaml > openfaas/11-role.yaml

kubectl get clusterrole > openfaas/12-clusterrole-list.txt
kubectl get clusterrole -o yaml > openfaas/13-clusterrole.yaml

kubectl get serviceaccount -n openfaas > openfaas/14-serviceaccount-list.txt
kubectl get serviceaccount -n openfaas -o yaml > openfaas/15-serviceaccount.yaml

echo ""

NAME=openfaas-$(date '+%Y-%m-%d_%H_%M_%S').tgz

echo Creating $NAME...

tar -czvf ./$NAME openfaas/
