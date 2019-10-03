package main

import (
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
	flags        = flag.NewFlagSet("hydrate", flag.ExitOnError)
	region       = flags.String("region", "", "AWS region")
	format       = flags.String("format", "", "input file format (json, yaml, toml, k8s)")
	debug        = flags.Bool("debug", false, "print debug info to stderr")
	enableBase64 = flags.Bool("base64", false, "base64 decode/encode all string value fields automatically (useful for Kubernetes Secrets/ConfigMap object data)")

	usage = errors.New(`hydrate:
Hydrate string values matching ^\$SECRET: regex with values from AWS SSM Param Store.

usage:
	# hydrate JSON file
		hydrate pstore in.json > secret.json

	# hydrate YAML data from stdin
		echo "data: $SECRET:/app/sit1/app_secret_data_key" | hydrate pstore --format=yml - > secret.yml

	# hydrate data/files in Kubernetes Secrets/ConfigMap objects
		hydrate --format=k8s pstore k8s-secret.yml | kubectl apply -
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
		ssm:    ssm.New(sess, aws.NewConfig()),
		base64: *enableBase64,
		debug:  *debug,
	}

	var data map[string]interface{}
	switch *format {
	case "json":
		dec := json.NewDecoder(r)
		if err := dec.Decode(&data); err != nil {
			log.Fatal(err)
		}
		if err := paramStore.Hydrate(data); err != nil {
			log.Fatal(err)
		}
		enc := json.NewEncoder(os.Stdout)
		if err := enc.Encode(data); err != nil {
			log.Fatal(err)
		}

	case "yml", "yaml":
		dec := yaml.NewDecoder(r)
		if err := dec.Decode(&data); err != nil {
			log.Fatal(err)
		}
		if err := paramStore.Hydrate(data); err != nil {
			log.Fatal(err)
		}
		enc := yaml.NewEncoder(os.Stdout)
		if err := enc.Encode(data); err != nil {
			log.Fatal(err)
		}

	case "toml":
		b, err := ioutil.ReadAll(r)
		if err != nil {
			log.Fatal(err)
		}
		if err := toml.Unmarshal(b, &data); err != nil {
			log.Fatal(err)
		}
		if err := paramStore.Hydrate(data); err != nil {
			log.Fatal(err)
		}
		dec := toml.NewEncoder(os.Stdout)
		if err := dec.Encode(data); err != nil {
			log.Fatal(err)
		}

	default:
		log.Fatal(fmt.Errorf("unknown file type: %v", filename))
	}
}

type paramStore struct {
	ssm    *ssm.SSM
	base64 bool
	debug  bool
}

var secretWithValueRegex = regexp.MustCompile(`^\$SECRET:`)

func (ps *paramStore) Hydrate(data map[string]interface{}) error {
	return ps.hydrate(data, nil)
}

func (ps *paramStore) hydrate(data map[string]interface{}, path []string) error {
	for key, value := range data {
		switch v := value.(type) {
		case string:
			if ps.base64 {
				decoded, err := base64.StdEncoding.DecodeString(v)
				if err != nil {
					fmt.Fprintf(os.Stderr, "couldn't decode %v value\n", key)
					continue
				}
				v = string(decoded)
				fmt.Fprintf(os.Stderr, "decoded %v value!!\n", key)
			}

			// Match secret values and fetch from Param Store.
			switch {
			case secretWithValueRegex.MatchString(v):
				secret := v[8:]
				if ps.debug {
					fmt.Fprintf(os.Stderr, "going to fetch %q secret", aws.String(secret))
				}

				param, err := ps.ssm.GetParameter(&ssm.GetParameterInput{
					Name:           aws.String(secret),
					WithDecryption: aws.Bool(true),
				})
				if err != nil {
					return errors.Wrapf(err, "failed to fetch param %q", secret)
				}

				if ps.base64 {
					data[key] = base64.StdEncoding.EncodeToString([]byte(*param.Parameter.Value))
				} else {
					data[key] = param.Parameter.Value
				}
			}

		case map[string]interface{}:
			// Recursively go deeper.
			if err := ps.hydrate(v, append(path, key)); err != nil {
				return err
			}
		}
	}
	return nil
}
