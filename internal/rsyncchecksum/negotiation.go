package rsyncchecksum

import (
	"fmt"
	"strings"
)

// SupportedAlgorithms returns the list of checksum algorithms supported in Protocol 30/31.
func SupportedAlgorithms() []string {
	return []string{"xxhash", "sha1", "md4", "md5"}
}

// NegotiateChecksumAlgorithm picks the first mutually supported algorithm.
func NegotiateChecksumAlgorithm(clientList string) (string, error) {
	if clientList == "" {
		return "md4", nil
	}
	parts := strings.Split(clientList, " ")
	supported := SupportedAlgorithms()

	for _, clientAlgo := range parts {
		clientAlgo = strings.TrimSpace(clientAlgo)
		for _, supp := range supported {
			if strings.EqualFold(clientAlgo, supp) {
				return supp, nil
			}
		}
	}
	return "", fmt.Errorf("no supported checksum algorithm found in client list: %q", clientList)
}
