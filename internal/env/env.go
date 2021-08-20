package env

import "os"

func SlackToken() string {
	return os.Getenv("SLACK_TOKEN")
}

func CloudformationEndpoint() string {
	cf, ok := os.LookupEnv("CLOUDFORMATION_ENDPOINT")
	if ok {
		return cf
	}
	return "cloudformation.us-east-1.amazonaws.com"
}

func SlackChannel() string {
	return os.Getenv("SLACK_CHANNEL")
}

func SlackHeader() string {
	return os.Getenv("SLACK_HEADER")
}
