#!/usr/bin/env bash
set -euxo pipefail

version=${1}
echo "generating version: $version"

echo "Generating the manifests using the helm..."
manifest="manifests-$version.yaml"

echo "Exporting manifests via helm ..."
helm template eso ../charts/external-secrets > ${manifest}

controller_image=$(yq e '.spec.template.spec.containers[].image' ${manifest} 2>/dev/null | grep external-secrets)

echo "generating csv ..."
./generate.js "${manifest}" "${version}" "${controller_image}"

echo "Bundle with operator-sdk ..."
operator-sdk bundle validate --select-optional name=operatorhub --verbose "bundles/${version}"

echo "Clean up ..."
rm "${manifest}"
