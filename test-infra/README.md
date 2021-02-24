# Test Infra


## Setup cloud provider
``` bash
# prepare your AWS credentials
export AWS_PROFILE=xyz

terraform init
terraform plan
terraform apply
```


## External-Secrets Operator | Cloud Integration

AWS: Place a file with the following content in `./testfiles/aws/.credentials`
These credentials should map to a user that is able to read from SecretsManager!

```
AWS_ACCESS_KEY_ID=XXXXXX
AWS_SECRET_ACCESS_KEY=YYYYYYY
```


## Running tests

```bash
# deploy controller with secrets an tests
kustomize build ./testfiles/aws/ | kubectl apply -f -

# check if controller runs fine:
kubectl get po -A

# check if secret was created and has the correct values
kubectl get secret aws-simple-string -o yaml | grep val
  json-binary-value: eyJteWtleSI6InZhbGwiLCJvYmplY3Rfa2V5Ijp7Im5lc3RlZF9rZXkiOiJuZXN0ZWRfdmFsIn19Cg==
  json-binary-value-mykey: dmFsbA==
  json-string-value: eyJteWtleSI6InZhbGwiLCJvYmplY3Rfa2V5Ijp7Im5lc3RlZF9rZXkiOiJuZXN0ZWRfdmFsIn19Cg==
  json-string-value-mykey: dmFsbA==
  simple-binary-value: Zm9vYW8K
  simple-string-value: Zm9vYW8K
[...]
```
