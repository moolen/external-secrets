mkfile_path := $(abspath $(lastword $(MAKEFILE_LIST)))
current_dir := $(notdir $(patsubst %/,%,$(dir $(mkfile_path))))
BUNDLE_IMAGE = $(IMAGE_REGISTRY):bundle-$(VERSION)

.PHONY: prepare
prepare:
	npm install

.PHONY: generate
generate: prepare
	./generate.sh $(VERSION)

.PHONY: image.build
image.build:
	docker build -t $(BUNDLE_IMAGE) \
		-f Dockerfile \
		bundles/$(VERSION)

.PHONY: image.push
image.push:
	docker push $(BUNDLE_IMAGE)

.PHONY: operatorhub.prepare
operatorhub.prepare:
	git clone https://github.com/k8s-operatorhub/community-operators
	mkdir -p community-operators/operators/external-secrets
	cp -r bundles/$(VERSION) community-operators/operators/external-secrets
	yq e -i '.channels[0].currentCSV = "external-secrets.v$(VERSION)"' community-operators/operators/external-secrets/external-secrets.package.yaml
	cd community-operators && \
		git status && \
		git format-patch --stdout HEAD^1
	@echo "prepared community-operators repo at $(current_dir)/community-operators"