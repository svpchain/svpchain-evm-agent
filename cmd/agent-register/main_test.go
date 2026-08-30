package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitTags(t *testing.T) {
	require.Equal(t, []string{"evm.swap", "evm.bridge"}, splitTags("evm.swap,evm.bridge"))
	require.Equal(t, []string{"evm.swap", "evm.bridge"}, splitTags(" evm.swap , evm.bridge "))
	require.Equal(t, []string{"evm.swap"}, splitTags("evm.swap,,"))
	require.Nil(t, splitTags(""))
	require.Nil(t, splitTags(" , "))
}

func TestParsePricing(t *testing.T) {
	t.Run("complete price", func(t *testing.T) {
		pricing, err := parsePricing(opts{
			pricingAmount: "1000000",
			pricingUnit:   "call",
		})
		require.NoError(t, err)
		require.Equal(t, "1000000", pricing.Amount)
		require.Equal(t, "call", pricing.Unit)
	})

	t.Run("all fields omitted preserves existing price", func(t *testing.T) {
		pricing, err := parsePricing(opts{})
		require.NoError(t, err)
		require.Nil(t, pricing)
	})

	t.Run("blank unit is refused", func(t *testing.T) {
		_, err := parsePricing(opts{pricingAmount: "1000000"})
		require.ErrorContains(t, err, "pricing-unit")
	})

	t.Run("zero amount is refused", func(t *testing.T) {
		_, err := parsePricing(opts{pricingAmount: "0", pricingUnit: "call"})
		require.ErrorContains(t, err, "positive")
	})
}

// The card advertises the interface URL the agent built from its own
// public_url. If that disagrees with the endpoint being registered, the
// registration would publish a URL whose card sends callers elsewhere.
func TestCheckCardURL(t *testing.T) {
	card := func(url string) []byte {
		return []byte(`{"supportedInterfaces":[{"url":"` + url + `"}]}`)
	}

	t.Run("matching base URL", func(t *testing.T) {
		require.NoError(t, checkCardURL(card("https://a.example/invoke"), "https://a.example"))
	})

	t.Run("different host is refused", func(t *testing.T) {
		err := checkCardURL(card("https://other.example/invoke"), "https://a.example")
		require.ErrorContains(t, err, "public_url and -url disagree")
	})

	// A prefix match on the bare string would accept this; the separator is
	// what stops "https://a.example.evil.com" passing for "https://a.example".
	t.Run("host that merely starts the same is refused", func(t *testing.T) {
		err := checkCardURL(card("https://a.example.evil.com/invoke"), "https://a.example")
		require.Error(t, err)
	})

	t.Run("card with no interfaces is not judged", func(t *testing.T) {
		require.NoError(t, checkCardURL([]byte(`{}`), "https://a.example"))
	})

	t.Run("unparseable card is an error", func(t *testing.T) {
		require.ErrorContains(t, checkCardURL([]byte("not json"), "https://a.example"), "parse agent card")
	})
}
