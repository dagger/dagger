package netinst

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/knftables"
)

// fakeIPTables keeps rules in memory and lists them the way `iptables -S`
// does: "-P"/"-N" first, then "-A <chain> <args>", quoting arguments with
// spaces.
type fakeIPTables struct {
	chains map[string][][]string // "table/chain" -> rules
}

func newFakeIPTables() *fakeIPTables {
	f := &fakeIPTables{chains: map[string][][]string{}}
	for _, c := range []string{"filter/INPUT", "filter/FORWARD", "filter/OUTPUT", "nat/PREROUTING", "nat/INPUT", "nat/OUTPUT", "nat/POSTROUTING"} {
		f.chains[c] = nil
	}
	return f
}

func (f *fakeIPTables) rules(table, chain string) ([][]string, error) {
	rules, ok := f.chains[table+"/"+chain]
	if !ok {
		return nil, fmt.Errorf("no chain %s in %s", chain, table)
	}
	return rules, nil
}

func (f *fakeIPTables) ChainExists(table, chain string) (bool, error) {
	_, ok := f.chains[table+"/"+chain]
	return ok, nil
}

func (f *fakeIPTables) NewChain(table, chain string) error {
	if _, ok := f.chains[table+"/"+chain]; ok {
		return fmt.Errorf("chain %s already exists", chain)
	}
	f.chains[table+"/"+chain] = nil
	return nil
}

func (f *fakeIPTables) ClearChain(table, chain string) error {
	f.chains[table+"/"+chain] = nil
	return nil
}

func (f *fakeIPTables) ClearAndDeleteChain(table, chain string) error {
	delete(f.chains, table+"/"+chain)
	return nil
}

func (f *fakeIPTables) Exists(table, chain string, rulespec ...string) (bool, error) {
	rules, err := f.rules(table, chain)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(rules, func(r []string) bool { return slices.Equal(r, rulespec) }), nil
}

func (f *fakeIPTables) Insert(table, chain string, pos int, rulespec ...string) error {
	rules, err := f.rules(table, chain)
	if err != nil {
		return err
	}
	f.chains[table+"/"+chain] = slices.Insert(rules, pos-1, rulespec)
	return nil
}

func (f *fakeIPTables) Append(table, chain string, rulespec ...string) error {
	rules, err := f.rules(table, chain)
	if err != nil {
		return err
	}
	f.chains[table+"/"+chain] = append(rules, rulespec)
	return nil
}

func (f *fakeIPTables) List(table, chain string) ([]string, error) {
	rules, err := f.rules(table, chain)
	if err != nil {
		return nil, err
	}
	head := "-N " + chain
	if strings.ToUpper(chain) == chain && !strings.Contains(chain, "-") {
		head = "-P " + chain + " ACCEPT"
	}
	lines := []string{head}
	for _, r := range rules {
		args := []string{"-A", chain}
		for _, a := range r {
			if strings.ContainsAny(a, ` "`) {
				a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
			}
			args = append(args, a)
		}
		lines = append(lines, strings.Join(args, " "))
	}
	return lines, nil
}

func (f *fakeIPTables) DeleteById(table, chain string, id int) error {
	rules, err := f.rules(table, chain)
	if err != nil {
		return err
	}
	f.chains[table+"/"+chain] = slices.Delete(rules, id-1, id)
	return nil
}

// dump renders a chain like `iptables -S <chain>` without the head line.
func (f *fakeIPTables) dump(t *testing.T, table, chain string) []string {
	t.Helper()
	lines, err := f.List(table, chain)
	require.NoError(t, err)
	return lines[1:]
}

func testNetwork(t *testing.T) *subnetNetwork {
	t.Helper()
	n, err := newSubnetNetwork("dagger", "10.87.0.0/16")
	require.NoError(t, err)
	return n
}

// The generated CNI config must not add per-container NAT or firewall rules:
// InstallSubnetRules covers the whole subnet instead.
func TestCNIConfigHasNoPerContainerRules(t *testing.T) {
	t.Parallel()
	raw, err := cniConfig("dagger", "10.87.0.0/16")
	require.NoError(t, err)
	var conf struct {
		Plugins []map[string]any `json:"plugins"`
	}
	require.NoError(t, json.Unmarshal(raw, &conf))
	var types []string
	for _, p := range conf.Plugins {
		types = append(types, p["type"].(string))
	}
	require.Equal(t, []string{"bridge", "dnsname"}, types)
	require.Equal(t, false, conf.Plugins[0]["ipMasq"])
	require.NotContains(t, conf.Plugins[0], "ipMasqBackend")
}

var (
	wantForwardJump = `-A FORWARD -m comment --comment "CNI firewall plugin rules" -j CNI-FORWARD`
	wantCNIForward  = []string{
		`-A CNI-FORWARD -m comment --comment "CNI firewall plugin admin overrides" -j CNI-ADMIN`,
		`-A CNI-FORWARD -s 10.87.0.0/16 -i dagger0 -m comment --comment "dagger subnet rules: dagger" -j ACCEPT`,
		`-A CNI-FORWARD -d 10.87.0.0/16 -o dagger0 -m conntrack --ctstate RELATED,ESTABLISHED -m comment --comment "dagger subnet rules: dagger" -j ACCEPT`,
	}
	wantMasqJump = `-A POSTROUTING -s 10.87.0.0/16 -m comment --comment "dagger subnet rules: dagger" -j DAGGER-MASQ-`
)

func TestInstallSubnetRulesIPTables(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	n := testNetwork(t)
	ipt := newFakeIPTables()

	require.NoError(t, installSubnetRules(ctx, n, ipt, nil, nil))

	require.Equal(t, []string{wantForwardJump}, ipt.dump(t, "filter", "FORWARD"))
	require.Equal(t, wantCNIForward, ipt.dump(t, "filter", "CNI-FORWARD"))
	require.Empty(t, ipt.dump(t, "filter", "CNI-ADMIN"))

	chain := n.masqChain()
	require.Len(t, chain, 28)
	require.Equal(t, []string{wantMasqJump + chain[len("DAGGER-MASQ-"):]}, ipt.dump(t, "nat", "POSTROUTING"))
	require.Equal(t, []string{
		`-A ` + chain + ` -s 10.87.0.1/32 -m comment --comment "dagger subnet rules: dagger" -j RETURN`,
		`-A ` + chain + ` -d 10.87.0.0/16 -m comment --comment "dagger subnet rules: dagger" -j ACCEPT`,
		`-A ` + chain + ` ! -d 224.0.0.0/4 -m comment --comment "dagger subnet rules: dagger" -j MASQUERADE`,
	}, ipt.dump(t, "nat", chain))

	// An engine restarting in the same network namespace changes nothing.
	before := fmt.Sprint(ipt.chains)
	require.NoError(t, installSubnetRules(ctx, n, ipt, nil, nil))
	require.Equal(t, before, fmt.Sprint(ipt.chains))
}

// Rules an administrator put ahead of the CNI jumps stay ahead, and existing
// jumps are not moved or duplicated.
func TestInstallSubnetRulesKeepsAdminPrecedence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	n := testNetwork(t)
	ipt := newFakeIPTables()
	adminDrop := []string{"-s", "10.87.0.9/32", "-j", "DROP"}
	require.NoError(t, ipt.Append("filter", "FORWARD", adminDrop...))
	require.NoError(t, ipt.Append("filter", "FORWARD", "-m", "comment", "--comment", cniForwardComment, "-j", cniForwardChain))
	require.NoError(t, ipt.NewChain("filter", cniForwardChain))
	require.NoError(t, ipt.NewChain("filter", cniAdminChain))
	require.NoError(t, ipt.Append("filter", cniAdminChain, adminDrop...))
	require.NoError(t, ipt.Append("nat", "POSTROUTING", "-s", "10.87.0.0/16", "-d", "192.0.2.0/24", "-j", "RETURN"))

	require.NoError(t, installSubnetRules(ctx, n, ipt, nil, nil))

	require.Equal(t, []string{`-A FORWARD -s 10.87.0.9/32 -j DROP`, wantForwardJump}, ipt.dump(t, "filter", "FORWARD"))
	require.Equal(t, wantCNIForward, ipt.dump(t, "filter", "CNI-FORWARD"))
	require.Equal(t, []string{`-A CNI-ADMIN -s 10.87.0.9/32 -j DROP`}, ipt.dump(t, "filter", "CNI-ADMIN"))
	postrouting := ipt.dump(t, "nat", "POSTROUTING")
	require.Len(t, postrouting, 2)
	require.Equal(t, `-A POSTROUTING -s 10.87.0.0/16 -d 192.0.2.0/24 -j RETURN`, postrouting[0])
	require.True(t, strings.HasPrefix(postrouting[1], wantMasqJump))
}

// Own rules from an earlier engine with a different subnet are replaced.
func TestInstallSubnetRulesReplacesStaleOwnRules(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ipt := newFakeIPTables()
	old, err := newSubnetNetwork("dagger", "10.88.0.0/16")
	require.NoError(t, err)
	require.NoError(t, installSubnetRules(ctx, old, ipt, nil, nil))

	n := testNetwork(t)
	require.NoError(t, installSubnetRules(ctx, n, ipt, nil, nil))

	require.Equal(t, wantCNIForward, ipt.dump(t, "filter", "CNI-FORWARD"))
	postrouting := ipt.dump(t, "nat", "POSTROUTING")
	require.Len(t, postrouting, 1)
	require.True(t, strings.HasPrefix(postrouting[0], wantMasqJump))
}

// Per-container rules older engines added for this network are removed; rules
// of other networks are left alone.
func TestInstallSubnetRulesCleansUpPerContainerRules(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	n := testNetwork(t)
	ipt := newFakeIPTables()
	// Bridge plugin ipMasq, for this network and for another one.
	for _, c := range []struct{ net, ip, chain string }{
		{"dagger", "10.87.0.2", "CNI-aaaaaaaaaaaaaaaaaaaaaaaa"},
		{"podman", "10.88.0.2", "CNI-bbbbbbbbbbbbbbbbbbbbbbbb"},
		{"dagger", "10.87.0.3", "CNI-cccccccccccccccccccccccc"},
	} {
		require.NoError(t, ipt.NewChain("nat", c.chain))
		require.NoError(t, ipt.Append("nat", c.chain, "-d", c.ip+"/16", "-j", "ACCEPT"))
		require.NoError(t, ipt.Append("nat", "POSTROUTING", "-s", c.ip+"/32", "-m", "comment", "--comment", fmt.Sprintf("name: %q id: %q", c.net, "ctr-"+c.ip), "-j", c.chain))
	}
	// Firewall plugin per-IP rules, in and out of the subnet.
	require.NoError(t, ipt.NewChain("filter", cniForwardChain))
	require.NoError(t, ipt.Append("filter", cniForwardChain, "-m", "comment", "--comment", cniAdminComment, "-j", cniAdminChain))
	for _, ip := range []string{"10.87.0.2", "10.88.0.2", "10.87.0.3"} {
		require.NoError(t, ipt.Append("filter", cniForwardChain, "-d", ip+"/32", "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"))
		require.NoError(t, ipt.Append("filter", cniForwardChain, "-s", ip+"/32", "-j", "ACCEPT"))
	}
	// Bridge plugin nftables masquerade, for this network and another one.
	cniNFT := knftables.NewFake(knftables.InetFamily, cniNFTMasqTable)
	tx := cniNFT.NewTransaction()
	tx.Add(&knftables.Table{})
	tx.Add(&knftables.Chain{Name: cniNFTMasqChain})
	for _, net := range []string{"dagger", "podman"} {
		comment := cniNFTRuleComment(net, "eth0", "ctr-1")
		tx.Add(&knftables.Rule{Chain: cniNFTMasqChain, Rule: "ip saddr 10.87.0.2 masquerade", Comment: &comment})
	}
	require.NoError(t, cniNFT.Run(ctx, tx))

	require.NoError(t, installSubnetRules(ctx, n, ipt, nil, cniNFT))

	postrouting := ipt.dump(t, "nat", "POSTROUTING")
	require.Len(t, postrouting, 2)
	require.Equal(t, `-A POSTROUTING -s 10.88.0.2/32 -m comment --comment "name: \"podman\" id: \"ctr-10.88.0.2\"" -j CNI-bbbbbbbbbbbbbbbbbbbbbbbb`, postrouting[0])
	require.True(t, strings.HasPrefix(postrouting[1], wantMasqJump))
	for chain, exists := range map[string]bool{
		"CNI-aaaaaaaaaaaaaaaaaaaaaaaa": false,
		"CNI-bbbbbbbbbbbbbbbbbbbbbbbb": true,
		"CNI-cccccccccccccccccccccccc": false,
	} {
		got, err := ipt.ChainExists("nat", chain)
		require.NoError(t, err)
		require.Equal(t, exists, got, chain)
	}
	require.Equal(t, []string{
		wantCNIForward[0],
		`-A CNI-FORWARD -d 10.88.0.2/32 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT`,
		`-A CNI-FORWARD -s 10.88.0.2/32 -j ACCEPT`,
		wantCNIForward[1],
		wantCNIForward[2],
	}, ipt.dump(t, "filter", "CNI-FORWARD"))
	rules, err := cniNFT.ListRules(ctx, cniNFTMasqChain)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	require.True(t, strings.Contains(*rules[0].Comment, "net: podman"))
}

func TestInstallSubnetRulesNFTables(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	n := testNetwork(t)
	ipt := newFakeIPTables()
	nft := knftables.NewFake(knftables.IPv4Family, n.nftTable())

	require.NoError(t, installSubnetRules(ctx, n, ipt, nft, nil))

	// Forwarding still goes through the iptables command (the nft variant on
	// these hosts), like the firewall plugin did.
	require.Equal(t, wantCNIForward, ipt.dump(t, "filter", "CNI-FORWARD"))
	require.Empty(t, ipt.dump(t, "nat", "POSTROUTING"))

	table := "ip " + n.nftTable()
	want := strings.Join([]string{
		`add table ` + table + ` { comment "Dagger engine masquerade for 10.87.0.0/16" ; }`,
		`add chain ` + table + ` postrouting { type nat hook postrouting priority 100 ; }`,
		`add rule ` + table + ` postrouting ip saddr 10.87.0.1 return comment "dagger subnet rules: dagger"`,
		`add rule ` + table + ` postrouting ip daddr 224.0.0.0/4 return comment "dagger subnet rules: dagger"`,
		`add rule ` + table + ` postrouting ip saddr 10.87.0.0/16 ip daddr != 10.87.0.0/16 masquerade comment "dagger subnet rules: dagger"`,
	}, "\n") + "\n"
	require.Equal(t, want, nft.Dump())

	// Idempotent: the chain is flushed and refilled in one transaction.
	require.NoError(t, installSubnetRules(ctx, n, ipt, nft, nil))
	require.Equal(t, want, nft.Dump())
}

func TestSplitRule(t *testing.T) {
	t.Parallel()
	args, err := splitRule(`-A POSTROUTING -s 10.87.0.2/32 -m comment --comment "name: \"dagger\" id: \"x y\"" -j CNI-abc`)
	require.NoError(t, err)
	require.Equal(t, []string{"-A", "POSTROUTING", "-s", "10.87.0.2/32", "-m", "comment", "--comment", `name: "dagger" id: "x y"`, "-j", "CNI-abc"}, args)
	_, err = splitRule(`-A X --comment "unterminated`)
	require.Error(t, err)
}

// cniNFTRuleComment builds the comment the CNI bridge plugin (v1.9.0,
// pkg/ip/ipmasq_nftables_linux.go commentForInstance) puts on its nftables
// masquerade rules.
func cniNFTRuleComment(network, ifname, containerID string) string {
	return fmt.Sprintf("%s-%s, net: %s, if: %s, id: %s",
		sha512Hex(network, 16), sha512Hex(ifname+":"+containerID, 16), network, ifname, containerID)
}
