#!/usr/bin/env node
// taken from here: https://github.com/weaveworks/flux2-openshift/blob/main/release.js

const YAML = require("yaml")
const fs = require("fs")
const glob = require("glob")
const { exit } = require("process")

// read manifest file passed as argument
const manifestFileName = process.argv[2]
const version = process.argv[3]
const file = fs.readFileSync(manifestFileName, "utf8")
const documents = YAML.parseAllDocuments(file)

// containerImage for CSV
const CONTROLLER_IMAGE = process.argv[4]

const kindMap = {
  Role: "role",
  RoleBinding: "rolebinding",
  ClusterRoleBinding: "clusterrolebinding",
  Deployment: "deployment",
  CustomResourceDefinition: "crd",
  Service: "service",
  ClusterRole: "clusterrole",
  ServiceAccount: "serviceaccount",
}

// setup directory for new version
const packagePath = "./bundles"
const newVersionDir = `${packagePath}/${version}/`

if (!fs.existsSync(newVersionDir)) {
  fs.mkdirSync(newVersionDir)
}
const manifestsDir = `${newVersionDir}/manifests`
if (!fs.existsSync(manifestsDir)) {
  fs.mkdirSync(manifestsDir)
}
const metadataDir = `${newVersionDir}/metadata`
if (!fs.existsSync(metadataDir)) {
  fs.mkdirSync(metadataDir)
}

// update annotations
const annotations = YAML.parse(
  fs.readFileSync("./annotations.yaml", "utf-8")
)
fs.writeFileSync(`${metadataDir}/annotations.yaml`, YAML.stringify(annotations))
const csv = YAML.parse(
  fs.readFileSync("./clusterserviceversion.yaml", "utf-8")
)
const deployments = []
const crds = []
let clusterRules = []
documents
  .filter((d) => d.contents)
  .map((d) => YAML.parse(String(d)))
  .filter((o) => o.kind !== "NetworkPolicy" && o.kind !== "Namespace") // not supported by operator-sdk
  .map((o) => {
    delete o.metadata.namespace
    switch (o.kind) {
      case "Role":
      case "RoleBinding":
      case "ClusterRoleBinding":
      case "ClusterRole":
          if (!o.metadata.name.endsWith("-controller") || o.rules === undefined) {
              return
          }
          clusterRules = clusterRules.concat(o.rules)
          break
      case "Service":
      case "ServiceAccount":
      case "Deployment":
        if(o.spec == null) {
          break
        }
        let deployment = {
          name: o.metadata.name,
          label: o.metadata.labels,
          spec: o.spec,
        }
        deployments.push(deployment)
        break
      case "CustomResourceDefinition":
        crds.push(o)
        const crdFileName = `${o.spec.names.singular}.${kindMap[o.kind]}.yaml`
        fs.writeFileSync(`${manifestsDir}/${crdFileName}`, YAML.stringify(o))
        break
      default:
        console.warn(
          "UNSUPPORTED KIND - you must explicitly ignore it or handle it",
          o.kind,
          o.metadata.name
        )
        process.exit(1)
        break
    }
  })

// Update ClusterServiceVersion
csv.spec.install.spec.deployments = deployments
csv.spec.install.spec.clusterPermissions[0].rules = clusterRules
csv.metadata.name = `external-secrets.v${version}`
csv.metadata.annotations.containerImage = CONTROLLER_IMAGE
csv.spec.version = version.replace(/^v/, '') // prefix is not valid, see: https://github.com/operator-framework/operator-sdk/issues/5342
csv.spec.minKubeVersion = "1.18.0"
csv.spec.maturity = "stable"
csv.spec.customresourcedefinitions.owned = []

crds.forEach((crd) => {
  crd.spec.versions.forEach((v) => {
    csv.spec.customresourcedefinitions.owned.push({
      name: crd.metadata.name,
      displayName: crd.spec.names.kind,
      kind: crd.spec.names.kind,
      version: v.name,
      description: crd.spec.names.kind,
    })
  })
})

const csvFileName = `external-secrets.v${version}.clusterserviceversion.yaml`
fs.writeFileSync(`${manifestsDir}/${csvFileName}`, YAML.stringify(csv))
