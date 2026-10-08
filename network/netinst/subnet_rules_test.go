package netinst

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
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
	chains    map[string][][]string // "table/chain" -> rules
	listErr   map[string]error      // "table/chain" -> error for the next List
	afterList map[string]func()     // "table/chain" -> run once after the next List
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
	if err := f.listErr[table+"/"+chain]; err != nil {
		delete(f.listErr, table+"/"+chain)
		return nil, err
	}
	rules, err := f.rules(table, chain)
	if err != nil {
		return nil, err
	}
	if hook := f.afterList[table+"/"+chain]; hook != nil {
		delete(f.afterList, table+"/"+chain)
		defer hook()
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

func (f *fakeIPTables) DeleteIfExists(table, chain string, rulespec ...string) error {
	rules, err := f.rules(table, chain)
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(rules, func(r []string) bool { return slices.Equal(r, rulespec) }); i >= 0 {
		f.chains[table+"/"+chain] = slices.Delete(rules, i, i+1)
	}
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

func noNFTSubnets(context.Context) (map[int]*net.IPNet, error) { return nil, nil }

// The generated CNI config adds no per-container NAT rules: InstallSubnetRules
// covers the whole subnet. The firewall plugin is kept only where firewalld
// manages the network.
func TestCNIConfig(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		firewallPlugin bool
		want           []string
	}{
		{firewallPlugin: false, want: []string{"bridge", "dnsname"}},
		{firewallPlugin: true, want: []string{"bridge", "firewall", "dnsname"}},
	} {
		t.Run(fmt.Sprintf("firewallPlugin=%t", tc.firewallPlugin), func(t *testing.T) {
			t.Parallel()
			raw, err := cniConfig("dagger", "10.87.0.0/16", tc.firewallPlugin)
			require.NoError(t, err)
			var conf struct {
				Plugins []map[string]any `json:"plugins"`
			}
			require.NoError(t, json.Unmarshal(raw, &conf))
			var types []string
			for _, p := range conf.Plugins {
				types = append(types, p["type"].(string))
			}
			require.Equal(t, tc.want, types)
			require.Equal(t, false, conf.Plugins[0]["ipMasq"])
			require.NotContains(t, conf.Plugins[0], "ipMasqBackend")
		})
	}
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

	require.NoError(t, installSubnetRules(ctx, n, ipt, nil, noNFTSubnets, false, true))

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
	require.NoError(t, installSubnetRules(ctx, n, ipt, nil, noNFTSubnets, false, true))
	require.Equal(t, before, fmt.Sprint(ipt.chains))
}

// With the firewall plugin kept for firewalld, no forward rules are installed,
// and ones installed before are removed; masquerading is still subnet-wide.
func TestInstallSubnetRulesWithoutForward(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	n := testNetwork(t)

	ipt := newFakeIPTables()
	require.NoError(t, installSubnetRules(ctx, n, ipt, nil, noNFTSubnets, false, false))
	require.Empty(t, ipt.dump(t, "filter", "FORWARD"))
	exists, err := ipt.ChainExists("filter", cniForwardChain)
	require.NoError(t, err)
	require.False(t, exists)
	require.Len(t, ipt.dump(t, "nat", "POSTROUTING"), 1)
	require.Len(t, ipt.dump(t, "nat", n.masqChain()), 3)

	// firewalld appeared after an engine installed subnet forward rules.
	ipt = newFakeIPTables()
	require.NoError(t, installSubnetRules(ctx, n, ipt, nil, noNFTSubnets, false, true))
	require.NoError(t, installSubnetRules(ctx, n, ipt, nil, noNFTSubnets, false, false))
	require.Equal(t, []string{wantForwardJump}, ipt.dump(t, "filter", "FORWARD"))
	require.Equal(t, wantCNIForward[:1], ipt.dump(t, "filter", "CNI-FORWARD"))
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

	require.NoError(t, installSubnetRules(ctx, n, ipt, nil, noNFTSubnets, false, true))

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
	require.NoError(t, installSubnetRules(ctx, old, ipt, nil, noNFTSubnets, false, true))

	n := testNetwork(t)
	require.NoError(t, installSubnetRules(ctx, n, ipt, nil, noNFTSubnets, false, true))

	require.Equal(t, wantCNIForward, ipt.dump(t, "filter", "CNI-FORWARD"))
	postrouting := ipt.dump(t, "nat", "POSTROUTING")
	require.Len(t, postrouting, 1)
	require.True(t, strings.HasPrefix(postrouting[0], wantMasqJump))
}

// addOldBridgeMasq adds what the CNI bridge plugin's iptables ipMasq added for
// one container.
func addOldBridgeMasq(t *testing.T, ipt *fakeIPTables, network, subnet, ip, chain string) {
	t.Helper()
	comment := fmt.Sprintf("name: %q id: %q", network, "ctr-"+ip)
	require.NoError(t, ipt.NewChain("nat", chain))
	require.NoError(t, ipt.Append("nat", chain, "-d", subnet, "-m", "comment", "--comment", comment, "-j", "ACCEPT"))
	require.NoError(t, ipt.Append("nat", chain, "!", "-d", "224.0.0.0/4", "-m", "comment", "--comment", comment, "-j", "MASQUERADE"))
	require.NoError(t, ipt.Append("nat", "POSTROUTING", "-s", ip+"/32", "-m", "comment", "--comment", comment, "-j", chain))
}

// addOldFirewallRules adds what the CNI firewall plugin added for one
// container.
func addOldFirewallRules(t *testing.T, ipt *fakeIPTables, ip string) {
	t.Helper()
	if exists, _ := ipt.ChainExists("filter", cniForwardChain); !exists {
		require.NoError(t, ipt.NewChain("filter", cniForwardChain))
		require.NoError(t, ipt.Append("filter", cniForwardChain, "-m", "comment", "--comment", cniAdminComment, "-j", cniAdminChain))
	}
	require.NoError(t, ipt.Append("filter", cniForwardChain, "-d", ip+"/32", "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"))
	require.NoError(t, ipt.Append("filter", cniForwardChain, "-s", ip+"/32", "-j", "ACCEPT"))
}

// Per-container rules older engines added for this network are removed, also
// for a subnet the network used before; rules of other networks are left alone.
func TestInstallSubnetRulesCleansUpPerContainerRules(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	n := testNetwork(t)
	ipt := newFakeIPTables()
	addOldBridgeMasq(t, ipt, "dagger", "10.87.0.0/16", "10.87.0.2", "CNI-aaaaaaaaaaaaaaaaaaaaaaaa")
	addOldBridgeMasq(t, ipt, "podman", "10.99.0.0/16", "10.99.0.2", "CNI-bbbbbbbbbbbbbbbbbbbbbbbb")
	// The same network, before its subnet changed.
	addOldBridgeMasq(t, ipt, "dagger", "10.88.0.0/16", "10.88.0.3", "CNI-cccccccccccccccccccccccc")
	for _, ip := range []string{"10.87.0.2", "10.99.0.2", "10.88.0.3"} {
		addOldFirewallRules(t, ipt, ip)
	}

	require.NoError(t, installSubnetRules(ctx, n, ipt, nil, noNFTSubnets, false, true))

	postrouting := ipt.dump(t, "nat", "POSTROUTING")
	require.Len(t, postrouting, 2)
	require.Equal(t, `-A POSTROUTING -s 10.99.0.2/32 -m comment --comment "name: \"podman\" id: \"ctr-10.99.0.2\"" -j CNI-bbbbbbbbbbbbbbbbbbbbbbbb`, postrouting[0])
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
		`-A CNI-FORWARD -d 10.99.0.2/32 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT`,
		`-A CNI-FORWARD -s 10.99.0.2/32 -j ACCEPT`,
		wantCNIForward[1],
		wantCNIForward[2],
	}, ipt.dump(t, "filter", "CNI-FORWARD"))
}

// If the subnets of leftover rules cannot be read, the leftovers stay, so the
// firewall rules for those subnets can still be found on the next start.
func TestInstallSubnetRulesRetriesCleanup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	n := testNetwork(t)

	t.Run("iptables", func(t *testing.T) {
		t.Parallel()
		ipt := newFakeIPTables()
		addOldBridgeMasq(t, ipt, "dagger", "10.88.0.0/16", "10.88.0.3", "CNI-cccccccccccccccccccccccc")
		addOldFirewallRules(t, ipt, "10.88.0.3")
		ipt.listErr = map[string]error{"nat/CNI-cccccccccccccccccccccccc": fmt.Errorf("busy")}

		require.NoError(t, installSubnetRules(ctx, n, ipt, nil, noNFTSubnets, false, true))
		require.Len(t, ipt.dump(t, "nat", "POSTROUTING"), 2)
		require.Contains(t, ipt.dump(t, "filter", "CNI-FORWARD"), `-A CNI-FORWARD -s 10.88.0.3/32 -j ACCEPT`)

		require.NoError(t, installSubnetRules(ctx, n, ipt, nil, noNFTSubnets, false, true))
		require.Len(t, ipt.dump(t, "nat", "POSTROUTING"), 1)
		require.Equal(t, wantCNIForward, ipt.dump(t, "filter", "CNI-FORWARD"))
	})

	t.Run("nftables", func(t *testing.T) {
		t.Parallel()
		ipt := newFakeIPTables()
		addOldFirewallRules(t, ipt, "10.88.0.2")
		nft, handles := newCNINFT(t, cniNFTRuleComment("dagger", "eth0", "ctr-1"))
		oldSubnet := &net.IPNet{IP: net.IPv4(10, 88, 0, 0).To4(), Mask: net.CIDRMask(16, 32)}
		failing := func(context.Context) (map[int]*net.IPNet, error) { return nil, fmt.Errorf("nft unavailable") }
		working := func(context.Context) (map[int]*net.IPNet, error) {
			return map[int]*net.IPNet{handles[0]: oldSubnet}, nil
		}

		require.NoError(t, installSubnetRules(ctx, n, ipt, nft, failing, true, true))
		rules, err := nft.ListRules(ctx, cniNFTMasqChain)
		require.NoError(t, err)
		require.Len(t, rules, 2) // the leftover and ours
		require.Contains(t, ipt.dump(t, "filter", "CNI-FORWARD"), `-A CNI-FORWARD -s 10.88.0.2/32 -j ACCEPT`)

		require.NoError(t, installSubnetRules(ctx, n, ipt, nft, working, true, true))
		rules, err = nft.ListRules(ctx, cniNFTMasqChain)
		require.NoError(t, err)
		require.Len(t, rules, 1)
		require.Equal(t, n.comment(), *rules[0].Comment)
		require.Equal(t, wantCNIForward, ipt.dump(t, "filter", "CNI-FORWARD"))
	})
}

// Leftovers are deleted by specification: if another writer changes the
// chain between listing and deleting, as other CNI plugins or administrators
// can in a shared network namespace, unrelated rules must survive.
func TestInstallSubnetRulesConcurrentChainChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	n := testNetwork(t)
	ipt := newFakeIPTables()
	addOldFirewallRules(t, ipt, "10.99.0.2") // another network's container
	addOldFirewallRules(t, ipt, "10.87.0.2") // an older engine's container
	adminDrop := []string{"-s", "10.50.0.9/32", "-j", "DROP"}
	require.NoError(t, ipt.Append("filter", cniForwardChain, adminDrop...))
	// Another writer removes a rule right after the chain is listed, shifting
	// every later rule up by one: the listed positions of the leftovers now
	// point at the second leftover and at the DROP.
	ipt.afterList = map[string]func(){"filter/" + cniForwardChain: func() {
		require.NoError(t, ipt.DeleteIfExists("filter", cniForwardChain, "-s", "10.99.0.2/32", "-j", "ACCEPT"))
	}}

	require.NoError(t, installSubnetRules(ctx, n, ipt, nil, noNFTSubnets, false, true))

	require.Equal(t, []string{
		wantCNIForward[0],
		`-A CNI-FORWARD -d 10.99.0.2/32 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT`,
		`-A CNI-FORWARD -s 10.50.0.9/32 -j DROP`,
		wantCNIForward[1],
		wantCNIForward[2],
	}, ipt.dump(t, "filter", "CNI-FORWARD"))
}

// newCNINFT returns a fake of the bridge plugin's nftables masquerade table
// holding rules with the given comments, and the handles they got.
func newCNINFT(t *testing.T, comments ...string) (*knftables.Fake, []int) {
	t.Helper()
	nft := knftables.NewFake(knftables.InetFamily, cniNFTMasqTable)
	tx := nft.NewTransaction()
	tx.Add(&knftables.Table{Comment: knftables.PtrTo(cniNFTMasqTableComment)})
	tx.Add(&knftables.Chain{Name: cniNFTMasqChain, Comment: knftables.PtrTo(cniNFTMasqChainComment)})
	for _, comment := range comments {
		rule := "ip daddr 192.0.2.0/24 return"
		if comment != "" {
			rule = "ip saddr 10.88.0.2 ip daddr != 10.88.0.0/16 masquerade"
		}
		r := &knftables.Rule{Chain: cniNFTMasqChain, Rule: rule}
		if comment != "" {
			r.Comment = knftables.PtrTo(comment)
		}
		tx.Add(r)
	}
	require.NoError(t, nft.Run(context.Background(), tx))
	var handles []int
	for _, r := range nft.Table.Chains[cniNFTMasqChain].Rules {
		handles = append(handles, *r.Handle)
	}
	return nft, handles
}

func TestInstallSubnetRulesNFTables(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	n := testNetwork(t)
	ipt := newFakeIPTables()
	addOldFirewallRules(t, ipt, "10.88.0.2")
	addOldFirewallRules(t, ipt, "10.99.0.2")
	// An administrator's exception, a rule an older engine added for this
	// network's container in its old subnet, and another network's rule.
	nft, handles := newCNINFT(t, "",
		cniNFTRuleComment("dagger", "eth0", "ctr-1"),
		cniNFTRuleComment("podman", "eth0", "ctr-2"))
	oldSubnet := &net.IPNet{IP: net.IPv4(10, 88, 0, 0).To4(), Mask: net.CIDRMask(16, 32)}
	nftSubnets := func(context.Context) (map[int]*net.IPNet, error) {
		return map[int]*net.IPNet{handles[1]: oldSubnet, handles[2]: oldSubnet}, nil
	}

	require.NoError(t, installSubnetRules(ctx, n, ipt, nft, nftSubnets, true, true))

	// Forwarding still goes through the iptables command (the nft variant on
	// these hosts), like the firewall plugin did. The old rules for the
	// network's earlier subnet are gone.
	require.Equal(t, []string{
		wantCNIForward[0],
		`-A CNI-FORWARD -d 10.99.0.2/32 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT`,
		`-A CNI-FORWARD -s 10.99.0.2/32 -j ACCEPT`,
		wantCNIForward[1],
		wantCNIForward[2],
	}, ipt.dump(t, "filter", "CNI-FORWARD"))
	require.Empty(t, ipt.dump(t, "nat", "POSTROUTING"))

	table := "inet " + cniNFTMasqTable
	want := strings.Join([]string{
		`add table ` + table + ` { comment "` + cniNFTMasqTableComment + `" ; }`,
		`add chain ` + table + ` masq_checks { comment "` + cniNFTMasqChainComment + `" ; }`,
		`add chain ` + table + ` postrouting { type nat hook postrouting priority 100 ; }`,
		`add rule ` + table + ` masq_checks ip daddr 192.0.2.0/24 return`,
		`add rule ` + table + ` masq_checks ip saddr 10.88.0.2 ip daddr != 10.88.0.0/16 masquerade comment "` + cniNFTRuleComment("podman", "eth0", "ctr-2") + `"`,
		`add rule ` + table + ` masq_checks ip saddr 10.87.0.0/16 ip saddr != 10.87.0.1 ip daddr != 10.87.0.0/16 masquerade comment "dagger subnet rules: dagger"`,
		`add rule ` + table + ` postrouting ip daddr == 224.0.0.0/4  return`,
		`add rule ` + table + ` postrouting ip6 daddr == ff00::/8  return`,
		`add rule ` + table + ` postrouting goto masq_checks`,
	}, "\n") + "\n"
	require.Equal(t, want, nft.Dump())

	// Idempotent, and the rule keeps its place: an exception the administrator
	// added after it stays after it.
	tx := nft.NewTransaction()
	tx.Add(&knftables.Rule{Chain: cniNFTMasqChain, Rule: "ip daddr 198.51.100.0/24 return"})
	require.NoError(t, nft.Run(ctx, tx))
	before := nft.Dump()
	require.Contains(t, before, "masquerade comment \"dagger subnet rules: dagger\"\nadd rule "+table+" masq_checks ip daddr 198.51.100.0/24 return\n")
	require.NoError(t, installSubnetRules(ctx, n, ipt, nft, nftSubnets, true, true))
	require.Equal(t, before, nft.Dump())
}

func TestParseNFTMasqSubnets(t *testing.T) {
	t.Parallel()
	// From `nft --json list chain inet cni_plugins_masquerade masq_checks`
	// after the CNI bridge plugin v1.9.0 added a container.
	out := `{"nftables": [{"metainfo": {"version": "1.1.1", "release_name": "Commodore Bullmoose #2", "json_schema_version": 1}}, {"chain": {"family": "inet", "table": "cni_plugins_masquerade", "name": "masq_checks", "handle": 2, "comment": "Masquerade traffic from certain IPs to any (non-multicast) IP outside their subnet"}}, {"rule": {"family": "inet", "table": "cni_plugins_masquerade", "chain": "masq_checks", "handle": 7, "comment": "x", "expr": [{"match": {"op": "==", "left": {"payload": {"protocol": "ip", "field": "saddr"}}, "right": "10.88.0.2"}}, {"match": {"op": "!=", "left": {"payload": {"protocol": "ip", "field": "daddr"}}, "right": {"prefix": {"addr": "10.88.0.0", "len": 16}}}}, {"masquerade": null}]}}]}`
	subnets, err := parseNFTMasqSubnets([]byte(out))
	require.NoError(t, err)
	require.Len(t, subnets, 1)
	require.Equal(t, "10.88.0.0/16", subnets[7].String())
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
		sha512Hex(network), sha512Hex(ifname+":"+containerID), network, ifname, containerID)
}
