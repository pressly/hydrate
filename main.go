package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ssm"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
)

var (
	flags  = flag.NewFlagSet("hydrate", flag.ExitOnError)
	region = flags.String("region", "", "AWS region")
	format = flags.String("format", "", "input file format (json, yaml, toml, k8s)")
	debug  = flags.Bool("debug", false, "print debug info to stderr")
	k8s    = flags.Bool("k8s", false, "hydrate Kubernetes Secret/ConfigMap objects' base64-encoded data fields")

	usage = errors.New(`hydrate:
Hydrate string values matching ^\$SECRET: regex with values from AWS SSM Param Store.

usage:
	# hydrate JSON file
		hydrate pstore in.json > secret.json

	# hydrate YAML data from stdin
		echo "data: $SECRET:/app/sit1/app_secret_data_key" | hydrate pstore --format=yml - > secret.yml

	# hydrate data/files in Kubernetes Secrets/ConfigMap objects
		hydrate -k8s pstore k8s-secret.yml | kubectl apply -
	`)
)

func main() {
	flags.Parse(os.Args[1:])

	if *region == "" {
		*region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if *region == "" {
		log.Fatal(errors.New("hydrate: --region=[us-west-2] or $AWS_DEFAULT_REGION must be provided"))
	}

	args := flags.Args()
	if len(args) != 2 || args[0] != "pstore" {
		log.Fatal(usage)
	}
	filename := args[1]

	var r io.Reader
	if filename == "-" {
		if *format == "" {
			log.Fatal(errors.New("hydrate: --format=[json|yaml|toml} must be provided when using STDIN"))
		}
		r = os.Stdin
	} else {
		if *format == "" {
			*format = strings.TrimLeft(filepath.Ext(filename), ".")
		}
		f, err := os.Open(filename)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		r = io.Reader(f)
	}

	sess, err := session.NewSession(&aws.Config{
		CredentialsChainVerboseErrors: aws.Bool(true),
		Region:                        region,
	})
	if err != nil {
		log.Fatal(errors.Wrap(err, "failed to create aws session"))
	}

	paramStore := &paramStore{
		ssm:   ssm.New(sess, aws.NewConfig()),
		debug: *debug,
	}

	data, err := getData(r, *format)
	if err != nil {
		log.Fatal(err)
	}

	if *k8s {
		if err := paramStore.HydrateK8s(data); err != nil {
			log.Fatal(err)
		}
	} else {
		if err := paramStore.Hydrate(data); err != nil {
			log.Fatal(err)
		}
	}

	if err := printData(os.Stdout, data, *format); err != nil {
		log.Fatal(err)
	}
}

type paramStore struct {
	ssm   *ssm.SSM
	debug bool
}

var secretWithValueRegex = regexp.MustCompile(`^\$SECRET:`)

func getData(r io.Reader, format string) (map[string]interface{}, error) {
	var data map[string]interface{}
	switch format {
	case "json":
		dec := json.NewDecoder(r)
		if err := dec.Decode(&data); err != nil {
			return nil, errors.Wrap(err, "failed to decode JSON")
		}

	case "yml", "yaml":
		dec := yaml.NewDecoder(r)
		if err := dec.Decode(&data); err != nil {
			return nil, errors.Wrap(err, "failed to decode YAML")
		}

	case "toml":
		b, err := ioutil.ReadAll(r)
		if err != nil {
			return nil, errors.Wrap(err, "failed to read TOML data")
		}
		if err := toml.Unmarshal(b, &data); err != nil {
			return nil, errors.Wrap(err, "failed to decode TOML")
		}

	default:
		return nil, fmt.Errorf("failed to hydrate: unknown file format %q", format)
	}

	return data, nil
}

func printData(w io.Writer, data map[string]interface{}, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		if err := enc.Encode(data); err != nil {
			return errors.Wrap(err, "failed to encode JSON")
		}

	case "yml", "yaml":
		enc := yaml.NewEncoder(w)
		if err := enc.Encode(data); err != nil {
			return errors.Wrap(err, "failed to encode YAML")
		}

	case "toml":
		enc := toml.NewEncoder(w)
		if err := enc.Encode(data); err != nil {
			return errors.Wrap(err, "failed to encode TOML")
		}

	default:
		return fmt.Errorf("failed to hydrate: unknown file format %q", format)
	}

	return nil
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

				data, err := getData(bytes.NewReader(decoded), format)
				if err != nil {
					log.Fatal(err)
				}

				if err := ps.Hydrate(data); err != nil {
					log.Fatal(err)
				}

				var b bytes.Buffer
				if err := printData(&b, data, format); err != nil {
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
