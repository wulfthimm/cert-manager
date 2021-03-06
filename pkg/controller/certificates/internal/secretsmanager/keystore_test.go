/*
Copyright 2020 The Jetstack cert-manager contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package secretsmanager

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"fmt"
	"testing"

	jks "github.com/pavel-v-chernykh/keystore-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"software.sslmate.com/src/go-pkcs12"

	cmapi "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1alpha2"
	"github.com/jetstack/cert-manager/pkg/util/pki"
)

func mustGeneratePrivateKey(t *testing.T, encoding cmapi.KeyEncoding) []byte {
	pk, err := pki.GenerateRSAPrivateKey(2048)
	if err != nil {
		t.Fatal(err)
	}
	pkBytes, err := pki.EncodePrivateKey(pk, encoding)
	if err != nil {
		t.Fatal(err)
	}
	return pkBytes
}

func mustSelfSignCertificate(t *testing.T, pkBytes []byte) []byte {
	if pkBytes == nil {
		pkBytes = mustGeneratePrivateKey(t, cmapi.PKCS8)
	}
	pk, err := pki.DecodePrivateKeyBytes(pkBytes)
	if err != nil {
		t.Fatal(err)
	}
	x509Crt, err := pki.GenerateTemplate(&cmapi.Certificate{
		Spec: cmapi.CertificateSpec{
			DNSNames: []string{"example.com"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	certBytes, _, err := pki.SignCertificate(x509Crt, x509Crt, pk.Public(), pk)
	if err != nil {
		t.Fatal(err)
	}
	return certBytes
}

type keyAndCert struct {
	key     crypto.Signer
	keyPEM  []byte
	cert    *x509.Certificate
	certPEM []byte
}

func mustCert(t *testing.T, commonName string, isCA bool) *keyAndCert {
	key, err := pki.GenerateRSAPrivateKey(2048)
	require.NoError(t, err)
	keyPEM, err := pki.EncodePrivateKey(key, cmapi.PKCS8)
	require.NoError(t, err)

	cert, err := pki.GenerateTemplate(&cmapi.Certificate{
		Spec: cmapi.CertificateSpec{
			CommonName: commonName,
			IsCA:       isCA,
		},
	})
	require.NoError(t, err)

	return &keyAndCert{
		key:    key,
		keyPEM: keyPEM,
		cert:   cert,
	}
}

func (o *keyAndCert) mustSign(t *testing.T, ca *keyAndCert) {
	require.True(t, ca.cert.IsCA, "not a CA", ca.cert)
	var err error
	o.certPEM, o.cert, err = pki.SignCertificate(o.cert, ca.cert, o.key.Public(), ca.key)
	require.NoError(t, err)
}

type certChain []*keyAndCert

func (o certChain) certsToPEM() (certs []byte) {
	for _, kc := range o {
		certs = append(certs, kc.certPEM...)
	}
	return
}

type leafWithChain struct {
	all  certChain
	leaf *keyAndCert
	cas  certChain
}

const chainLength = 3

func mustLeafWithChain(t *testing.T) leafWithChain {
	all := make(certChain, chainLength)

	var last *keyAndCert
	for i := range all {
		isCA := i > 0
		commonName := fmt.Sprintf("Cert %d of %d", i+1, chainLength)
		c := mustCert(t, commonName, isCA)
		if last != nil {
			last.mustSign(t, c)
		}
		last = c
		all[i] = c
	}
	last.mustSign(t, last)

	return leafWithChain{
		all:  all,
		leaf: all[0],
		cas:  all[1:],
	}
}

func TestEncodeJKSKeystore(t *testing.T) {
	tests := map[string]struct {
		password               string
		rawKey, certPEM, caPEM []byte
		verify                 func(t *testing.T, out []byte, err error)
	}{
		"encode a JKS bundle for a PKCS1 key and certificate only": {
			password: "password",
			rawKey:   mustGeneratePrivateKey(t, cmapi.PKCS1),
			certPEM:  mustSelfSignCertificate(t, nil),
			verify: func(t *testing.T, out []byte, err error) {
				if err != nil {
					t.Errorf("expected no error but got: %v", err)
					return
				}
				buf := bytes.NewBuffer(out)
				ks, err := jks.Decode(buf, []byte("password"))
				if err != nil {
					t.Errorf("error decoding keystore: %v", err)
					return
				}
				if ks["certificate"] == nil {
					t.Errorf("no certificate data found in keystore")
				}
				if ks["ca"] != nil {
					t.Errorf("unexpected ca data found in keystore")
				}
			},
		},
		"encode a JKS bundle for a PKCS8 key and certificate only": {
			password: "password",
			rawKey:   mustGeneratePrivateKey(t, cmapi.PKCS8),
			certPEM:  mustSelfSignCertificate(t, nil),
			verify: func(t *testing.T, out []byte, err error) {
				if err != nil {
					t.Errorf("expected no error but got: %v", err)
				}
				buf := bytes.NewBuffer(out)
				ks, err := jks.Decode(buf, []byte("password"))
				if err != nil {
					t.Errorf("error decoding keystore: %v", err)
					return
				}
				if ks["certificate"] == nil {
					t.Errorf("no certificate data found in keystore")
				}
				if ks["ca"] != nil {
					t.Errorf("unexpected ca data found in keystore")
				}
			},
		},
		"encode a JKS bundle for a key, certificate and ca": {
			password: "password",
			rawKey:   mustGeneratePrivateKey(t, cmapi.PKCS8),
			certPEM:  mustSelfSignCertificate(t, nil),
			caPEM:    mustSelfSignCertificate(t, nil),
			verify: func(t *testing.T, out []byte, err error) {
				if err != nil {
					t.Errorf("expected no error but got: %v", err)
				}
				buf := bytes.NewBuffer(out)
				ks, err := jks.Decode(buf, []byte("password"))
				if err != nil {
					t.Errorf("error decoding keystore: %v", err)
					return
				}
				if ks["certificate"] == nil {
					t.Errorf("no certificate data found in keystore")
				}
				if ks["ca"] == nil {
					t.Errorf("no ca data found in keystore")
				}
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			out, err := encodeJKSKeystore([]byte(test.password), test.rawKey, test.certPEM, test.caPEM)
			test.verify(t, out, err)
		})
	}
}

func TestEncodePKCS12Keystore(t *testing.T) {
	tests := map[string]struct {
		password               string
		rawKey, certPEM, caPEM []byte
		verify                 func(t *testing.T, out []byte, err error)
		run                    func(t testing.T)
	}{
		"encode a JKS bundle for a PKCS1 key and certificate only": {
			password: "password",
			rawKey:   mustGeneratePrivateKey(t, cmapi.PKCS1),
			certPEM:  mustSelfSignCertificate(t, nil),
			verify: func(t *testing.T, out []byte, err error) {
				if err != nil {
					t.Errorf("expected no error but got: %v", err)
				}
				pk, cert, err := pkcs12.Decode(out, "password")
				if err != nil {
					t.Errorf("error decoding keystore: %v", err)
					return
				}
				if cert == nil {
					t.Errorf("no certificate data found in keystore")
				}
				if pk == nil {
					t.Errorf("no ca data found in keystore")
				}
			},
		},
		"encode a JKS bundle for a PKCS8 key and certificate only": {
			password: "password",
			rawKey:   mustGeneratePrivateKey(t, cmapi.PKCS8),
			certPEM:  mustSelfSignCertificate(t, nil),
			verify: func(t *testing.T, out []byte, err error) {
				if err != nil {
					t.Errorf("expected no error but got: %v", err)
				}
				pk, cert, err := pkcs12.Decode(out, "password")
				if err != nil {
					t.Errorf("error decoding keystore: %v", err)
					return
				}
				if cert == nil {
					t.Errorf("no certificate data found in keystore")
				}
				if pk == nil {
					t.Errorf("no ca data found in keystore")
				}
			},
		},
		"encode a JKS bundle for a key, certificate and ca": {
			password: "password",
			rawKey:   mustGeneratePrivateKey(t, cmapi.PKCS8),
			certPEM:  mustSelfSignCertificate(t, nil),
			caPEM:    mustSelfSignCertificate(t, nil),
			verify: func(t *testing.T, out []byte, err error) {
				if err != nil {
					t.Errorf("expected no error but got: %v", err)
				}
				pk, cert, caCerts, err := pkcs12.DecodeChain(out, "password")
				if err != nil {
					t.Errorf("error decoding keystore: %v", err)
					return
				}
				if cert == nil {
					t.Errorf("no certificate data found in keystore")
				}
				if pk == nil {
					t.Errorf("no private key data found in keystore")
				}
				if caCerts == nil {
					t.Errorf("no ca data found in keystore")
				}
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			out, err := encodePKCS12Keystore(test.password, test.rawKey, test.certPEM, test.caPEM)
			test.verify(t, out, err)
		})
	}
	t.Run("encodePKCS12Keystore encodes non-leaf certificates to the CA certificate chain, even when the supplied CA chain is empty", func(t *testing.T) {
		const password = "password"
		var emptyCAChain []byte = nil

		chain := mustLeafWithChain(t)
		out, err := encodePKCS12Keystore(password, chain.leaf.keyPEM, chain.all.certsToPEM(), emptyCAChain)

		pkOut, certOut, caChain, err := pkcs12.DecodeChain(out, password)
		require.NoError(t, err)
		assert.NotNil(t, pkOut)
		assert.Equal(t, chain.leaf.cert.Signature, certOut.Signature, "leaf certificate signature does not match")
		if assert.Len(t, caChain, 2, "caChain should contain 2 items: intermediate certificate and top-level certificate") {
			assert.Equal(t, chain.cas[0].cert.Signature, caChain[0].Signature, "intermediate certificate signature does not match")
			assert.Equal(t, chain.cas[1].cert.Signature, caChain[1].Signature, "top-level certificate signature does not match")
		}
	})
	t.Run("encodePKCS12Keystore *prepends* non-leaf certificates to the supplied CA certificate chain", func(t *testing.T) {
		const password = "password"
		var caChainInPEM []byte = mustSelfSignCertificate(t, nil)
		caChainIn, err := pki.DecodeX509CertificateChainBytes(caChainInPEM)
		require.NoError(t, err)

		chain := mustLeafWithChain(t)
		out, err := encodePKCS12Keystore(password, chain.leaf.keyPEM, chain.all.certsToPEM(), caChainInPEM)

		pkOut, certOut, caChainOut, err := pkcs12.DecodeChain(out, password)
		require.NoError(t, err)
		assert.NotNil(t, pkOut)
		assert.Equal(t, chain.leaf.cert.Signature, certOut.Signature, "leaf certificate signature does not match")
		if assert.Len(t, caChainOut, 3, "caChain should contain 3 items: intermediate certificate and top-level certificate and supplied CA") {
			assert.Equal(t, chain.cas[0].cert.Signature, caChainOut[0].Signature, "intermediate certificate signature does not match")
			assert.Equal(t, chain.cas[1].cert.Signature, caChainOut[1].Signature, "top-level certificate signature does not match")
			assert.Equal(t, caChainIn, caChainOut[2:], "supplied certificate chain is not at the end of the chain")
		}
	})
}
