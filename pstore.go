package hydrate

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
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
		return errors.Errorf("hydrate: k8s object is of kind=%q (supported: ConfigMap, Secret)", kind)
	}

	metadata, ok := data["metadata"].(map[string]interface{})
	if !ok {
		return errors.Errorf("hydrate: k8s object of kind=%q doesn't have metadata", kind)
	}
	name, _ := metadata["name"].(string)

	for _, field := range []struct {
		name    string
		encoded bool
	}{
		{"data", kind == "secret"},
		{"stringData", false},
		{"binaryData", true},
	} {
		loopOver, _ := data[field.name].(map[string]interface{})
		for key, value := range loopOver {
			strValue, ok := value.(string)
			if !ok {
				fmt.Fprintf(os.Stderr, "hydrate: k8s %v/%v: failed to decode %v (kind %T)\n", kind, name, key, value)
				continue
			}

			var (
				valueReader io.Reader = strings.NewReader(strValue)
				b           bytes.Buffer
				valueWriter io.Writer = &b
			)
			if field.encoded {
				valueReader = base64.NewDecoder(base64.StdEncoding, valueReader)
				valueWriter = base64.NewEncoder(base64.StdEncoding, valueWriter)
			}

			format := strings.TrimLeft(filepath.Ext(key), ".")
			switch format {
			case "json", "yml", "yaml", "toml":
				fmt.Fprintf(os.Stderr, "hydrate: k8s %v/%v: %v (%v %v file, base64-encoded: %v)\n", kind, name, key, field.name, strings.ToUpper(format), field.encoded)

				err := ps.Hydrate(valueWriter, valueReader, format, false)
				if err != nil {
					return errors.Wrapf(err, "hydrate: k8s %v/%v: failed to hydrate %v", kind, name, key)
				}
				loopOver[key] = b.String()

			default:
				// Just a value, not a file.
				fmt.Fprintf(os.Stderr, "hydrate: k8s %v/%v: %v (%v value, base64-encoded: %v)\n", kind, name, key, field.name, field.encoded)

				var valBuf bytes.Buffer
				valBuf.ReadFrom(valueReader)
				if secret, err := ps.hydrateKeyValue(key, valBuf.String()); err != nil {
					return errors.Wrapf(err, "hydrate: k8s %v/%v: failed to hydrate %v", kind, name, key)
				} else if secret != nil {
					valueWriter.Write([]byte(*secret))
					loopOver[key] = b.String()
				}
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
