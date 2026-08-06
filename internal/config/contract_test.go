package config

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProductionEnvironmentContractFilesStayAligned(t *testing.T) {
	envTemplate := readContractFile(t, "../../.env.example")
	workflow := readContractFile(t, "../../.github/workflows/deploy.yml")
	prWorkflow := readContractFile(t, "../../.github/workflows/pr.yml")
	rotationWorkflow := readContractFile(t, "../../.github/workflows/key-rotation.yml")
	compose := readContractFile(t, "../../docker-compose.server.yaml")
	prod, err := readConfig("../../config/config.prod.yml")
	require.NoError(t, err)

	templateKeys := environmentKeys(envTemplate)
	workflowKeys := captureKeys(workflow, `(?m)^\s+CI_([A-Z][A-Z0-9_]+):`)
	composeKeys := captureKeys(compose, `\$\{([A-Z][A-Z0-9_]+)\}`)
	assert.Equal(t, templateKeys, workflowKeys, "deploy workflow must render every .env.example key")
	assert.Subset(t, templateKeys, composeKeys, "compose variables must belong to the canonical contract")

	applicationSecrets := map[string]string{
		"DB_URL": "db.url", "REDIS_URL": "redis.url",
		"MINIO_ACCESS_KEY": "minio.access_key", "MINIO_SECRET_KEY": "minio.secret_key",
		"JWT_SECRET": "jwt.secret", "CRYPTO_AES_KEY": "crypto.aes_key",
		"CRYPTO_HMAC_KEY": "crypto.hmac_key", "SMTP_PASSWORD": "smtp.password",
		"YANDEX_CLIENT_SECRET": "yandex.client_secret", "OPENAI_API_KEY": "openai.api_key",
	}
	for envName, configPath := range applicationSecrets {
		assert.Contains(t, templateKeys, envName, envName)
		assert.True(t, prod.IsSet(configPath), configPath)
	}

	assert.Contains(t, compose, "env_file:\n      - .env")
	assert.Contains(t, workflow, "scripts/render-deploy-env.sh")
	assert.Contains(t, workflow, "scripts/validate-deploy-env.sh")
	assert.NotContains(t, workflow, "pip3 install dump-env")
	assert.NotContains(t, workflow, "YANDEX_IAM_TOKEN")
	assert.NotContains(t, envTemplate, "PICTURES_BANK_TOKEN")
	assert.Contains(t, workflow, "audit-secret-fingerprints.sh")
	assert.Contains(t, prWorkflow, `git archive --format=tar "$HEAD_SHA"`)
	assert.Contains(t, prWorkflow, "dir --source=/repo")
	assert.Contains(t, prWorkflow, "fetch-depth: 0")
	assert.Contains(t, prWorkflow, `git rev-list --reverse "${merge_base}..${HEAD_SHA}"`)
	assert.Contains(t, prWorkflow, "Scan current pull request tree")
	assert.Contains(t, prWorkflow, "Scan every commit introduced by the pull request")
	assert.Contains(t, prWorkflow, "Verify history scan catches a secret removed later")
	assert.Contains(t, prWorkflow, "Crypto Rotation Integration")
	assert.Contains(t, prWorkflow, "Required PR checks")
	assert.Contains(t, rotationWorkflow, "environment: production")
	assert.Contains(t, rotationWorkflow, "ROTATE_CRYPTO_KEYS")
	assert.Contains(t, rotationWorkflow, "rotation_image:")
	assert.Contains(t, rotationWorkflow, "server@sha256:[0-9a-f]{64}")
	assert.Contains(t, rotationWorkflow, `docker pull "$ROTATION_IMAGE"`)
	assert.Contains(t, rotationWorkflow, `docker login ghcr.io`)
	assert.Contains(t, rotationWorkflow, `--project-directory "$PWD"`)
	assert.Contains(t, rotationWorkflow, `-f "$override_file"`)
	assert.Contains(t, rotationWorkflow, "packages: read")
	assert.Contains(t, workflow, "Publish immutable rotation image reference")
	assert.Contains(t, workflow, "steps.build_server.outputs.digest")
}

func TestDevFixturesAreExplicitAndProductionRejectsThem(t *testing.T) {
	dev := readContractFile(t, "../../config/config.dev.yml")
	assert.Contains(t, dev, "DEV ONLY")
	assert.Contains(t, dev, DevJWTSecret)
	assert.Contains(t, dev, DevAESKeyBase64)
	assert.Contains(t, dev, DevHMACKeyBase64)
	assert.Contains(t, dev, DevMinIOAccessKey)
	assert.Contains(t, dev, DevMinIOSecretKey)
}

func environmentKeys(content string) []string {
	keys := make([]string, 0)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		keys = append(keys, strings.SplitN(line, "=", 2)[0])
	}
	sort.Strings(keys)
	return keys
}

func captureKeys(content, pattern string) []string {
	re := regexp.MustCompile(pattern)
	matches := re.FindAllStringSubmatch(content, -1)
	keys := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if _, found := seen[match[1]]; found {
			continue
		}
		seen[match[1]] = struct{}{}
		keys = append(keys, match[1])
	}
	sort.Strings(keys)
	return keys
}

func readContractFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.ReplaceAll(string(content), "\r\n", "\n")
}
