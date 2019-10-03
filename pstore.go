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

type paramStore struct {
	ssm   *ssm.SSM
	debug bool
}

func ParamStore(ssm *ssm.SSM) *paramStore {
	return &paramStore{
		ssm: ssm,
	}
}

func (ps *paramStore) Debug(debug bool) {
	ps.debug = debug
}

func (ps *paramStore) Hydrate(data map[string]interface{}) error {
	return ps.hydrateRecursively(data, nil)
}

func (ps *paramStore) HydrateK8s(data map[string]interface{}) error {
	// Kubernetes object.
	kind, _ := data["kind"].(string)
	switch kind {
	case "ConfigMap", "Secret":
		// OK. Proceed.
	case "":
		// Empty kind, this doesn't look like k8s object at all.. let's treat it as regular file.
		return ps.hydrateRecursively(data, nil)
	default:
		return errors.Errorf("k8s object is of kind=%q (supported: ConfigMap, Secret)", kind)
	}

	// Hydrate all base64-encoded "data" fields.
	if data, ok := data["data"].(map[string]interface{}); ok {
		for key, value := range data {
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
				fmt.Fprintf(os.Stderr, "k8s: decoding base64-encoded data field %q file\n", key)

				data, err := GetData(bytes.NewReader(decoded), format)
				if err != nil {
					log.Fatal(err)
				}

				if err := ps.Hydrate(data); err != nil {
					log.Fatal(err)
				}

				var b bytes.Buffer
				if err := PrintData(&b, data, format); err != nil {
					log.Fatal(err)
				}

				data[key] = base64.StdEncoding.EncodeToString(b.Bytes())
			default:
				// Just a value, not a file.
				fmt.Fprintf(os.Stderr, "k8s: decoding base64-encoded data field %q value\n", key)

				if secret, err := ps.hydrateValue(string(decoded)); err != nil {
					return errors.Wrapf(err, "failed to hydrate k8s data field %q", key)
				} else if secret != nil {
					data[key] = base64.StdEncoding.EncodeToString([]byte(*secret))
				}
			}
		}
	}
	// TODO: Hydrate plain-text "stringData" fields.
	return nil
}

func (ps *paramStore) hydrateValue(v string) (*string, error) {
	// Match secret values and fetch from Param Store.
	switch {
	case secretWithValueRegex.MatchString(v):
		secret := v[8:]
		if ps.debug {
			fmt.Fprintf(os.Stderr, "\tfetching %q secret\n", secret)
		}

		param, err := ps.ssm.GetParameter(&ssm.GetParameterInput{
			Name:           aws.String(secret),
			WithDecryption: aws.Bool(true),
		})
		if err != nil {
			return nil, errors.Wrapf(err, "failed to fetch param %q", secret)
		}

		return param.Parameter.Value, nil
	}

	return nil, nil
}

func (ps *paramStore) hydrateRecursively(data map[string]interface{}, path []string) error {
	for key, value := range data {
		switch v := value.(type) {
		case string:
			if secret, err := ps.hydrateValue(v); err != nil {
				return errors.Wrapf(err, "failed to hydrate %q", strings.Join(append(path, key), "."))
			} else if secret != nil {
				data[key] = *secret
			}

		case map[string]interface{}:
			// Recursively go deeper.
			if err := ps.hydrateRecursively(v, append(path, key)); err != nil {
				return err
			}
		}
	}
	return nil
}
