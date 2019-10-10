package hydrate

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ssm"
	"github.com/pkg/errors"
)

//go:generate syncmap -pkg hydrate -name stringMap map[string]string

type paramStore struct {
	ssm      *ssm.SSM
	basePath string

	secrets stringMap
}

func ParamStore(ssm *ssm.SSM, basePath string) *paramStore {
	return &paramStore{
		ssm:      ssm,
		secrets:  stringMap{},
		basePath: basePath,
	}
}

func (ps *paramStore) GetSecret(key string) (string, error) {
	if !strings.HasPrefix(key, "/") {
		if ps.basePath == "" {
			return "", errors.Errorf("%q doesn't look like a valid parameter path, did you provide default path, ie. --path=/app/sit1/ ?", key)
		}
		if !strings.HasPrefix(key, ps.basePath) {
			fmt.Fprintf(os.Stderr, "\tWARNING: %q secret key doesn't match the base path %q\n", key, ps.basePath)
		}
		key = filepath.Join(ps.basePath, key)
	}

	if secret, ok := ps.secrets.Load(key); ok {
		return secret, nil
	}

	fmt.Fprintf(os.Stderr, "- fetching %q secret from AWS SSM Parameter Store\n", key)

	param, err := ps.ssm.GetParameter(&ssm.GetParameterInput{
		Name:           aws.String(key),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", errors.Wrapf(err, "failed to fetch %q parameter", key)
	}

	secret := *param.Parameter.Value
	ps.secrets.Store(key, secret)

	return secret, nil
}

func (ps *paramStore) hydrateData(data map[string]interface{}, k8s bool) error {
	if k8s {
		return ps.hydrateK8sObject(data)
	}
	return ps.hydrateMapRecursively(data, nil)
}

func (ps *paramStore) hydrateK8sObject(data map[string]interface{}) error {
	// Kubernetes object.
	kind, _ := data["kind"].(string)
	switch kind {
	case "ConfigMap", "Secret":
		kind = strings.ToLower(kind)
		// OK. Proceed.
	case "":
		// Empty kind, this doesn't look like k8s object at all.. let's treat it as regular file.
		return ps.hydrateMapRecursively(data, nil)
	default:
		return errors.Errorf("hydrate k8s object is of kind=%q (supported: ConfigMap, Secret)", kind)
	}

	metadata, ok := data["metadata"].(map[string]interface{})
	if !ok {
		return errors.Errorf("hydrate k8s object of kind=%q doesn't have metadata", kind)
	}
	name, _ := metadata["name"].(string)

	k8sData, ok1 := data["data"].(map[string]interface{})
	k8sStringData, ok2 := data["stringData"].(map[string]interface{})
	if !ok1 && !ok2 {
		fmt.Fprintf(os.Stderr, "hydrate k8s %v/%v: can't find \"data\" or \"stringData\" field\n", kind, name)
	}

	// Hydrate all base64-encoded "data" fields.
	for key, value := range k8sData {
		str, ok := value.(string)
		if !ok {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(str)
		if err != nil {
			return errors.Wrapf(err, "failed to base64-decode %q data field", key)
		}

		format := strings.TrimLeft(filepath.Ext(key), ".")

		switch format {
		case "json", "yml", "yaml", "toml":
			fmt.Fprintf(os.Stderr, "hydrate k8s %v/%v: data.%q (base64-encoded %v file data)\n", kind, name, key, strings.ToUpper(format))

			var b bytes.Buffer
			err := ps.Hydrate(&b, bytes.NewReader(decoded), format, false)
			if err != nil {
				log.Fatal(err)
			}
			k8sData[key] = base64.StdEncoding.EncodeToString(b.Bytes())

		default:
			// Just a value, not a file.
			fmt.Fprintf(os.Stderr, "hydrate k8s %v/%v: data.%q (base64-encoded value)\n", kind, name, key)

			if secret, err := ps.hydrateKeyValue(key, string(decoded)); err != nil {
				return errors.Wrapf(err, "failed to hydrate k8s data field %q", key)
			} else if secret != nil {
				k8sData[key] = base64.StdEncoding.EncodeToString([]byte(*secret))
			}
		}
	}

	// Hydrate plain-text "stringData" fields.
	for key, value := range k8sStringData {
		str, ok := value.(string)
		if !ok {
			continue
		}

		format := strings.TrimLeft(filepath.Ext(key), ".")

		switch format {
		case "json", "yml", "yaml", "toml":
			fmt.Fprintf(os.Stderr, "hydrate k8s %v/%v: hydrate stringData.%q (plain-text %v file data)\n", kind, name, key, strings.ToUpper(format))

			var b bytes.Buffer
			if err := ps.Hydrate(&b, strings.NewReader(str), format, false); err != nil {
				log.Fatal(err)
			}

			k8sStringData[key] = b.String()
		default:
			// Just a value, not a file.
			fmt.Fprintf(os.Stderr, "hydrate k8s %v/%v: hydrate stringData.%q (plain-text value)\n", kind, name, key)

			if secret, err := ps.hydrateKeyValue(key, str); err != nil {
				return errors.Wrapf(err, "failed to hydrate k8s data field %q", key)
			} else if secret != nil {
				k8sStringData[key] = *secret
			}
		}
	}

	return nil
}

func (ps *paramStore) hydrateKeyValue(key, value string) (*string, error) {
	// Match secret values and fetch from Param Store.
	switch {
	case value == "$$" || value == "$SECRET":
		secret, err := ps.GetSecret(key)
		if err != nil {
			return nil, errors.Wrapf(err, "%v=%q", key, value)
		}

		return &secret, nil

	case strings.HasPrefix(value, "$SECRET:"):
		secretKey := strings.TrimPrefix(value, "$SECRET:")

		secret, err := ps.GetSecret(secretKey)
		if err != nil {
			return nil, errors.Wrapf(err, "%v=%q", key, value)
		}

		return &secret, nil
	}

	return nil, nil
}

func (ps *paramStore) hydrateMapRecursively(data map[string]interface{}, path []string) error {
	for key, value := range data {
		switch v := value.(type) {
		case string:
			if secret, err := ps.hydrateKeyValue(key, v); err != nil {
				return errors.Wrapf(err, "failed to hydrate %q field", strings.Join(append(path, key), "."))
			} else if secret != nil {
				data[key] = *secret
			}

		case map[string]interface{}:
			// Recursively go deeper.
			if err := ps.hydrateMapRecursively(v, append(path, key)); err != nil {
				return err
			}
		}
	}
	return nil
}
