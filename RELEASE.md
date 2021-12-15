# Release Process

ESO and the ESO Helm Chart have two distinct lifecycles and can be released independently. Helm Chart releases are named `external-secrets-x.y.z`.

The external-secrets project is released on a as-needed basis. Feel free to open a issue to request a release.

## Release ESO

1. Run `Create Release` Action to create a new release, pass in the desired version number to release.
2. GitHub Release, Changelog will be created by the `release.yml` workflow which also promotes the container image.
3. update Helm Chart
4. generate OLM manifests and open a PR against [community-operators](https://github.com/k8s-operatorhub/community-operators)
5. Announce the new release in the `#external-secrets` Kubernetes Slack

## Release Helm Chart

1. Update `version` and/or `appVersion` in `Chart.yaml`
2. push and merge PR
3. CI picks up the new chart version and creates a new GitHub Release for it

## Generate OLM Manifests

```
$ git checkout <tag>
$ make olm.bundle
$ make olm.image.build
$ make olm.image.push
```

Use the following files and open a PR against [community-operators](https://github.com/k8s-operatorhub/community-operators) repository.

```
$ tree deploy/olm/bundles/<version>
deploy/olm/bundles/<version>
├── manifests
│   ├── clustersecretstore.crd.yaml
│   ├── externalsecret.crd.yaml
│   ├── external-secrets.v<version>.clusterserviceversion.yaml
│   └── secretstore.crd.yaml
└── metadata
    └── annotations.yaml
```

```
$ git clone https://github.com/k8s-operatorhub/community-operators
$ mkdir -p community-operators/operators/external-secrets
$ cp deploy/olm/bundles/<version> community-operators/operators/external-secrets
```