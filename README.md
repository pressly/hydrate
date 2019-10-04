# Hydrate secrets from AWS Systems Manager Parameter Store

Hydrate JSON, YAML, TOML config files.

Replace all matching string values with strings/secrets from AWS SSM Param Store.
1. `"$SECRET:/custom/parameter/path"`
2. `"$$"`
3. `"$SECRET"`

## Usage:
### Hydrate JSON file:
    hydrate no-secrets.json > secrets.json

### Hydrate YAML data from stdin:
    echo "data: $SECRET:/app/sit1/app_secret_data_key" | hydrate --format=yml - > secret.yml

### Hydrate Kubernetes Secrets/ConfigMap data files/values
Hydrate both "data" and "stringData" fields (handles base64 encoding automatically).

    hydrate -k8s k8s-secret.yml | kubectl apply -

## Example:

### Parameter Store:
    PATH                     VALUE
    /custom/parameter/path   a
    /prefix/db_passwd        bb
    /prefix/db_pwd           ccc
    
### input.json:
    {
        "db_password": "$SECRET:/custom/parameter/path",
        "db_passwd": "$$",
        "db_pwd": "$SECRET",
    }
	
### Run command:
    hydrate --path=/prefix input.json > output.json

### output.json:
    {
        "db_password": "a",
        "db_passwd": "bb",
        "db_pwd": "ccc",
    }
