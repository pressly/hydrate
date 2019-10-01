package main

import (
	"encoding/json"
	"flag"
	"fmt"
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
	"gopkg.in/yaml.v2"
)

var (
	flags  = flag.NewFlagSet("hydrate", flag.ExitOnError)
	pod    = flags.String("pod", "", "VC POD, ie. sit1")
	region = flags.String("region", "", "AWS region")
)

// TODO: How do we

func main() {
	flags.Parse(os.Args[1:])

	if *pod == "" {
		log.Fatal(errors.New("--pod=$POD option is required"))
	}
	if *region == "" {
		*region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if *region == "" {
		log.Fatal(errors.New("either --region= option or $AWS_DEFAULT_REGION env var is required"))
	}

	args := flags.Args()
	if len(args) != 1 {
		log.Fatal(errors.New("single filename argument is required"))
	}
	filename := args[0]

	f, err := os.Open(filename)
	if err != nil {
		log.Fatal(err)
	}

	sess, err := session.NewSession(&aws.Config{
		CredentialsChainVerboseErrors: aws.Bool(true),
		Region:                        region,
	})
	if err != nil {
		log.Fatal(errors.Wrap(err, "failed to create aws session"))
	}

	paramStore := &paramStore{
		ssm: ssm.New(sess, aws.NewConfig()),
	}

	var data map[string]interface{}
	switch filepath.Ext(filename) {
	case ".json":
		dec := json.NewDecoder(f)
		if err := dec.Decode(&data); err != nil {
			log.Fatal(err)
		}
		if err := paramStore.Hydrate(&data); err != nil {
			log.Fatal(err)
		}
		enc := json.NewEncoder(os.Stdout)
		if err := enc.Encode(data); err != nil {
			log.Fatal(err)
		}

	case ".yml", ".yaml":
		dec := yaml.NewDecoder(f)
		if err := dec.Decode(&data); err != nil {
			log.Fatal(err)
		}
		if err := paramStore.Hydrate(&data); err != nil {
			log.Fatal(err)
		}
		enc := yaml.NewEncoder(os.Stdout)
		if err := enc.Encode(data); err != nil {
			log.Fatal(err)
		}

	case ".toml":
		b, err := ioutil.ReadAll(f)
		if err != nil {
			log.Fatal(err)
		}
		if err := toml.Unmarshal(b, &data); err != nil {
			log.Fatal(err)
		}
		if err := paramStore.Hydrate(&data); err != nil {
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
	ssm *ssm.SSM
}

var secretWithValueRegex = regexp.MustCompile(`^\$SECRET:`)

func (ps *paramStore) Hydrate(data *map[string]interface{}) error {
	return ps.hydrate(data, nil)
}

func (ps *paramStore) hydrate(data *map[string]interface{}, path []string) error {
	for key, value := range *data {
		switch v := value.(type) {
		case string:
			// Match secret values and fetch from Param Store.
			switch {
			case secretWithValueRegex.MatchString(v):
				secret := v[8:]

				prefix := fmt.Sprintf("/app/%s/", *pod)
				if !strings.HasPrefix(secret, prefix) {
					return errors.Errorf("param %q doesn't match the $POD prefix %q", secret, prefix)
				}

				param, err := ps.ssm.GetParameter(&ssm.GetParameterInput{
					Name:           aws.String(secret),
					WithDecryption: aws.Bool(true),
				})
				if err != nil {
					return errors.Wrapf(err, "failed to fetch param %q", secret)
				}
				(*data)[key] = param.Parameter.Value
			default:
				fmt.Fprintf(os.Stderr, "nope, %q didn't match secret", v)
			}

		case map[string]interface{}:
			// Recursively go deeper.
			if err := ps.hydrate(&v, append(path, key)); err != nil {
				return err
			}
		}
	}
	return nil
}
