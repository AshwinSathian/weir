//go:build integration

package storetest

// integration is set by the integration build tag, under which remote
// stores run the cases that wait on the real clock (05 §8).
const integration = true
