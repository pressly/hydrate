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

### Hydrate Kubernetes Secrets/ConfigMap objects

    hydrate -k8s k8s-secret.yml | kubectl apply -

For each YAML object in the input file:
1. If object matches `kind: Secret`, hydrate `data` and `stringData` maps.
2. If object matches `kind: ConfigMap`, hydrate `data` and `binaryData` maps.
3. Else, leave the object untouched.

Hydrate automatically handles base64-encoded values and hydrates both plain values
and `.yml`, `.json` and `.toml` config files stored within the above maps.

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
