## Check your OpenFaaS Configuration

This is a diagnostic tool meant for OpenFaaS users to check the configuration of their Kubernetes cluster. It's written by the team that provide Enteprise Support to paying customers.

It runs just once (inside your cluster) to check your functions and the way OpenFaaS itself is configured.

See also: [Troubleshooting OpenFaaS](https://docs.openfaas.com/deployment/troubleshooting/)

## How it works

You deploy a one-time Kubernetes job that runs some queries against the Kubernetes API using [a limited RBAC definition](https://github.com/openfaas/config-checker/blob/master/artifacts/rbac.yaml). The results are printed to the pod's logs.

It's completely offline. There is no call-home or data transferred to any third-party.

You must email the results to us, or attach them to a Slack conversation.

## What's collected

OpenFaaS core components:

* Whether you're using OpenFaaS Pro
* Which image tags you're using
* Timeout values

Functions:

* Function names
* Auto-scaling settings
* The number of replicas for each function

## What's not collected

Confidential data, secrets, other environment variables.

## Run the job and collect the results (automated)

Download [run-job from GitHub](https://github.com/alexellis/run-job) or [use "arkade get"](https://arkade.dev/)

```bash
# Apply the RBAC file
kubectl apply -f https://raw.githubusercontent.com/openfaas/config-checker/master/artifacts/rbac.yaml

# Install run-job to run the job and collect the results
arkade get run-job

# Download the job definition
curl -SLs \
  -f https://raw.githubusercontent.com/openfaas/config-checker/master/job.yaml \
  -o /tmp/job.yaml

# Output to file with today's date
$HOME/.arkade/bin/run-job \
    -f /tmp/job.yaml \
    --out $(date '+%Y-%m-%d_%H_%M_%S').txt

# Or print to console:
$HOME/.arkade/bin/run-job -f /tmp/job.yaml

# run-job will try to run `pwd`/job.yaml, so you can also skip the argument
cd /tmp/
$HOME/.arkade/bin/run-job
```

## Run the job and collect the results (with kubectl only)

For run of the tool, run all steps 1-3.

1) Deploy the job:

```bash
#!/bin/bash

kubectl apply -f ./artifacts/
```

2) Collect the results from the logs:

```bash
#!/bin/bash

JOBNAME="checker"
JOBUUID=$(kubectl get job -n openfaas $JOBNAME -o "jsonpath={.metadata.labels.controller-uid}")
PODNAME=$(kubectl get po -n openfaas -l controller-uid=$JOBUUID -o name)

kubectl logs -n openfaas $PODNAME > $(date '+%Y-%m-%d_%H_%M_%S').txt
```

3) Remove the job and its RBAC permissions:

```bash
kubectl delete -f ./artifacts
```

## Making sense of the results

Feel free to get in touch with us to discuss the results: [contact us](https://openfaas.com/support)
