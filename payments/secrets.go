package payments

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/kms"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/brave-intl/bat-go/utils/cryptography"
	"github.com/brave-intl/bat-go/utils/logging"
)

// GetConfig - return the decrypted configuration for the payments service
func GetConfig(ctx context.Context, location, keyARN string) (io.Reader, error) {
	logger := logging.Logger(ctx, "payments.GetConfig")
	// parse the url, to figure out how to get it
	u, err := url.Parse(location)
	if err != nil {
		logger.Error().Err(err).Msg("failed to parse location")
		return nil, fmt.Errorf("failed to parse location: %w", err)
	}
	logger.Debug().Str("config-url", location).Str("key-arn", keyARN).Msg("attempting to get configuration")

	var c = []byte{}

	// config ciphertext supported protos: s3, file
	switch strings.ToLower(u.Scheme) {
	case "file":
		logger.Debug().
			Str("config-url", location).
			Str("key-arn", keyARN).Msg("configuration is file based")
		fmt.Println("!!! u.Path: ", u.Path)
		// read the configuration file
		c, err = os.ReadFile(u.Path)
		if err != nil {
			logger.Fatal().Err(err).
				Str("config-url", location).
				Str("key-arn", keyARN).Msg("unable to read configuration file")
		}
		fmt.Println("!!! c: ", hex.EncodeToString(c))
	case "s3":
		logger.Debug().
			Str("config-url", location).
			Str("key-arn", keyARN).Msg("configuration is s3 based")

		buf := aws.NewWriteAtBuffer([]byte{})
		// download from s3
		sess := session.Must(session.NewSession())
		downloader := s3manager.NewDownloader(sess)
		n, err := downloader.Download(buf, &s3.GetObjectInput{
			Bucket: aws.String(u.Host),
			Key:    aws.String(u.Path),
		})

		if err != nil {
			logger.Fatal().Err(err).
				Str("config-url", location).
				Str("key-arn", keyARN).Msg("unable to download configuration")
		}
		if n <= 0 {
			logger.Fatal().Err(err).
				Str("config-url", location).
				Str("key-arn", keyARN).Msg("file downloaded has no length, empty")
		}

	default:
		logger.Error().Msg("unsupported file location scheme")
		return nil, fmt.Errorf("unsupported file location scheme")
	}

	// if key-arn is not supplied, return the downloaded/read bytes
	if keyARN == "" {
		return bytes.NewBuffer(c), nil
	}

	// is the key specified as a local file?
	if strings.HasPrefix(keyARN, "file://") {
		// get the path
		u, err := url.Parse(keyARN)
		if err != nil {
			logger.Error().Err(err).Msg("failed to parse key location")
			return nil, fmt.Errorf("failed to parse key location")
		}
		// parse the key
		kf, err := os.Open(u.Path)
		if err != nil {
			logger.Error().Err(err).Msg("failed to open key file")
			return nil, fmt.Errorf("failed to open key file")
		}

		var (
			decryptNonce [24]byte
			key          [32]byte
			b            = make([]byte, 32)
		)

		_, err = kf.Read(b)
		if err != nil {
			logger.Error().Err(err).Msg("failed to read key")
			return nil, fmt.Errorf("failed to read key")
		}

		// get the key
		_, err = hex.Decode(key[:], b)
		if err != nil {
			logger.Error().Err(err).Msg("failed to parse key")
			return nil, fmt.Errorf("failed to parse key")
		}

		// nonce is first 24 bytes of ciphertext
		copy(decryptNonce[:], c[:24])
		fmt.Println("!!! nonce: ", hex.EncodeToString(decryptNonce[:]))

		// key is on local filesystem
		p, err := cryptography.DecryptMessage(key, c[24:], decryptNonce[:])
		if err != nil {
			logger.Error().Err(err).Msg("failed to decrypt config")
			return nil, fmt.Errorf("failed to decrypt config")
		}
		return bytes.NewBufferString(p), nil

	}
	session, err := session.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	// use kms to decrypt
	svc := kms.New(session)

	input := &kms.DecryptInput{
		CiphertextBlob: c,
		KeyId:          aws.String(keyARN),
	}

	result, err := svc.Decrypt(input)
	if err != nil {
		logger.Error().Err(err).Msg("failed to decrypt configuration")
		return nil, fmt.Errorf("failed to decrypt configuration: %w", err)
	}
	return bytes.NewBuffer(result.Plaintext), nil
}