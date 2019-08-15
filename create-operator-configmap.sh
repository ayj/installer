#!/bin/bash

set -ex

operator_data_dir=operator-data/
mkdir -p ${operator_data_dir}

dirs="crds security istio-control gateways istio-cni istiocoredns istio-policy istio-telemetry"

from_files=
for dir in ${dirs}; do
  tar cvfz ${dir}.tar.gz ${dir}
  from_files+=" --from-file=${dir}.tar.gz"
done
from_files+=" --from-file=global.yaml"

kubectl create configmap istio-oper ${from_files} --dry-run -o yaml > istio-operator-configmap.yaml

for dir in ${dirs}; do
  rm ${dir}.tar.gz
done
