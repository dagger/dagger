package netinst

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/coreos/go-iptables/iptables"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"sigs.k8s.io/knftables"
)

// The engine's CNI config has no per-container NAT or firewall rules (no
// bridge ipMasq, no firewall plugin). InstallSubnetRules installs the
// equivalent rules once for the whole container subnet instead.
//
// Per-container rules made every new network namespace run a series of
// iptables commands that serialize on the xtables lock and, with legacy
// iptables, rewrite whole tables that grow with every container. With
// hundreds of execs starting at once, that dominated exec start-up.

const (
	// Chains and rules the CNI firewall plugin installs, reused verbatim so the
	// CNI-ADMIN override hook keeps working and existing installs are not
	// duplicated.
	cniForwardChain     = "CNI-FORWARD"
	cniAdminChain       = "CNI-ADMIN"
	cniForwardComment   = "CNI firewall plugin rules"
	cniAdminComment     = "CNI firewall plugin admin overrides"
	multicastSubnetIPv4 = "224.0.0.0/4"

	// Where the CNI bridge plugin's nftables masquerade put per-container rules.
	cniNFTMasqTable = "cni_plugins_masquerade"
	cniNFTMasqChain = "masq_checks"
)

// iptablesClient is the subset of go-iptables used here.
type iptablesClient interface {
	ChainExists(table, chain string) (bool, error)
	NewChain(table, chain string) error
	ClearChain(table, chain string) error
	ClearAndDeleteChain(table, chain string) error
	Exists(table, chain string, rulespec ...string) (bool, error)
	Insert(table, chain string, pos int, rulespec ...string) error
	Append(table, chain string, rulespec ...string) error
	List(table, chain string) ([]string, error)
	DeleteById(table, chain string, id int) error
}

// subnetNetwork describes the engine's container network.
type subnetNetwork struct {
	name    string
	bridge  string
	subnet  *net.IPNet
	gateway net.IP
}

func newSubnetNetwork(name, cidr string) (*subnetNetwork, error) {
	_, subnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	if subnet.IP.To4() == nil {
		return nil, fmt.Errorf("subnet %s is not IPv4", cidr)
	}
	gateway := make(net.IP, 4)
	copy(gateway, subnet.IP.To4())
	gateway[3] = 1
	return &subnetNetwork{
		name:    name,
		bridge:  name + "0",
		subnet:  subnet,
		gateway: gateway,
	}, nil
}

// comment marks every rule installed for this network.
func (n *subnetNetwork) comment() string {
	return "dagger subnet rules: " + n.name
}

// masqChain is the iptables nat chain holding this network's masquerade rules.
// Chain names are limited to 28 characters.
func (n *subnetNetwork) masqChain() string {
	return "DAGGER-MASQ-" + sha512Hex(n.name, 16)
}

// nftTable is the nftables table holding this network's masquerade rules.
func (n *subnetNetwork) nftTable() string {
	return "dagger_masq_" + sha512Hex(n.name, 16)
}

// sha512Hex returns the first length hex digits of the SHA-512 of s, the
// hashing the CNI plugins use for chain names and rule comments.
func sha512Hex(s string, length int) string {
	sum := sha512.Sum512([]byte(s))
	return hex.EncodeToString(sum[:])[:length]
}

// forwardRules accept traffic from the containers and replies to it, like the
// CNI firewall plugin's per-IP rules, but for the whole subnet. Outbound
// traffic must arrive from the bridge; replies must leave through it.
//
// Rule specs here and below use the argument order `iptables -S` prints, so
// listed rules can be compared with them.
func (n *subnetNetwork) forwardRules() [][]string {
	return [][]string{
		{"-s", n.subnet.String(), "-i", n.bridge, "-m", "comment", "--comment", n.comment(), "-j", "ACCEPT"},
		{"-d", n.subnet.String(), "-o", n.bridge, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-m", "comment", "--comment", n.comment(), "-j", "ACCEPT"},
	}
}

// masqRules masquerade traffic from the subnet to anything outside it except
// multicast, like the CNI bridge plugin's per-container ipMasq chain. The
// engine's own traffic from the gateway address is left alone, as it was when
// the rules matched individual container IPs.
func (n *subnetNetwork) masqRules() [][]string {
	return [][]string{
		{"-s", n.gateway.String() + "/32", "-m", "comment", "--comment", n.comment(), "-j", "RETURN"},
		{"-d", n.subnet.String(), "-m", "comment", "--comment", n.comment(), "-j", "ACCEPT"},
		{"!", "-d", multicastSubnetIPv4, "-m", "comment", "--comment", n.comment(), "-j", "MASQUERADE"},
	}
}

func (n *subnetNetwork) masqJumpRule() []string {
	return []string{"-s", n.subnet.String(), "-m", "comment", "--comment", n.comment(), "-j", n.masqChain()}
}

// InstallSubnetRules installs the forwarding and masquerade rules for the
// engine's container network and removes per-container rules left by older
// engines in the same network namespace. It is idempotent.
//
// Forwarding rules always go through the iptables command, as the CNI firewall
// plugin's did; EnsureIptablesSymlinks points it at the legacy or nft variant.
// Masquerading uses the same backend the bridge plugin would have used.
func InstallSubnetRules(ctx context.Context, name, cidr string) error {
	n, err := newSubnetNetwork(name, cidr)
	if err != nil {
		return err
	}
	ipt, err := iptables.NewWithProtocol(iptables.ProtocolIPv4)
	if err != nil {
		return fmt.Errorf("iptables: %w", err)
	}
	backend := detectIPMasqBackend()
	bklog.G(ctx).Infof("using ipMasqBackend: %s", backend)
	var masqNFT knftables.Interface
	if backend == "nftables" {
		if masqNFT, err = knftables.New(knftables.IPv4Family, n.nftTable()); err != nil {
			return fmt.Errorf("nftables: %w", err)
		}
	}
	// Older engines on nft-only hosts used the bridge plugin's native
	// nftables masquerade.
	cniNFT, err := knftables.New(knftables.InetFamily, cniNFTMasqTable)
	if err != nil {
		bklog.G(ctx).WithError(err).Debug("nftables unavailable; skipping leftover nftables masquerade cleanup")
		cniNFT = nil
	}
	if err := installSubnetRules(ctx, n, ipt, masqNFT, cniNFT); err != nil {
		return err
	}
	bklog.G(ctx).Infof("installed subnet rules for %s (%s)", n.name, n.subnet)
	return nil
}

// installSubnetRules masquerades with masqNFT if set, else with iptables.
// cniNFT, if set, is the bridge plugin's nftables masquerade table to clean up.
func installSubnetRules(ctx context.Context, n *subnetNetwork, ipt iptablesClient, masqNFT, cniNFT knftables.Interface) error {
	if err := cleanupPerContainerIPTablesRules(ipt, n); err != nil {
		bklog.G(ctx).WithError(err).Warn("failed to remove leftover per-container iptables rules")
	}
	if cniNFT != nil {
		if err := cleanupPerContainerNFTRules(ctx, cniNFT, n); err != nil {
			bklog.G(ctx).WithError(err).Warn("failed to remove leftover per-container nftables rules")
		}
	}
	if err := ensureForwardRules(ipt, n); err != nil {
		return fmt.Errorf("install forward rules: %w", err)
	}
	if masqNFT != nil {
		if err := ensureNFTMasquerade(ctx, masqNFT, n); err != nil {
			return fmt.Errorf("install nftables masquerade: %w", err)
		}
		return nil
	}
	if err := ensureIPTablesMasquerade(ipt, n); err != nil {
		return fmt.Errorf("install iptables masquerade: %w", err)
	}
	return nil
}

func ensureChain(ipt iptablesClient, table, chain string) error {
	exists, err := ipt.ChainExists(table, chain)
	if err != nil || exists {
		return err
	}
	return ipt.NewChain(table, chain)
}

// ensureFirstRule inserts rule at the top of chain unless it is already in the
// chain, wherever it is. This is how the CNI firewall plugin places its jumps,
// so rules an administrator put ahead of them stay ahead.
func ensureFirstRule(ipt iptablesClient, table, chain string, rule []string) error {
	exists, err := ipt.Exists(table, chain, rule...)
	if err != nil || exists {
		return err
	}
	return ipt.Insert(table, chain, 1, rule...)
}

func ensureAppended(ipt iptablesClient, table, chain string, rule []string) error {
	exists, err := ipt.Exists(table, chain, rule...)
	if err != nil || exists {
		return err
	}
	return ipt.Append(table, chain, rule...)
}

func ensureForwardRules(ipt iptablesClient, n *subnetNetwork) error {
	for _, chain := range []string{cniForwardChain, cniAdminChain} {
		if err := ensureChain(ipt, "filter", chain); err != nil {
			return err
		}
	}
	if err := ensureFirstRule(ipt, "filter", "FORWARD", []string{"-m", "comment", "--comment", cniForwardComment, "-j", cniForwardChain}); err != nil {
		return err
	}
	// CNI-ADMIN goes first in CNI-FORWARD, so administrators can still
	// override the accepts below.
	if err := ensureFirstRule(ipt, "filter", cniForwardChain, []string{"-m", "comment", "--comment", cniAdminComment, "-j", cniAdminChain}); err != nil {
		return err
	}
	if err := deleteStaleOwnRules(ipt, "filter", cniForwardChain, n, n.forwardRules()); err != nil {
		return err
	}
	for _, rule := range n.forwardRules() {
		if err := ensureAppended(ipt, "filter", cniForwardChain, rule); err != nil {
			return err
		}
	}
	return nil
}

func ensureIPTablesMasquerade(ipt iptablesClient, n *subnetNetwork) error {
	chain := n.masqChain()
	if err := ensureChain(ipt, "nat", chain); err != nil {
		return err
	}
	// The chain is ours alone, so rewrite it to the current rules.
	if err := ipt.ClearChain("nat", chain); err != nil {
		return err
	}
	for _, rule := range n.masqRules() {
		if err := ipt.Append("nat", chain, rule...); err != nil {
			return err
		}
	}
	// Appended like the bridge plugin's per-container jumps, so POSTROUTING
	// rules installed before it keep their precedence.
	if err := deleteStaleOwnRules(ipt, "nat", "POSTROUTING", n, [][]string{n.masqJumpRule()}); err != nil {
		return err
	}
	return ensureAppended(ipt, "nat", "POSTROUTING", n.masqJumpRule())
}

// ensureNFTMasquerade replaces this network's nftables table with the current
// rules in one transaction. Like the bridge plugin's own table, it is a
// separate base chain at the postrouting hook.
func ensureNFTMasquerade(ctx context.Context, nft knftables.Interface, n *subnetNetwork) error {
	tx := nft.NewTransaction()
	tx.Add(&knftables.Table{
		Comment: knftables.PtrTo("Dagger engine masquerade for " + n.subnet.String()),
	})
	tx.Add(&knftables.Chain{
		Name:     "postrouting",
		Type:     knftables.PtrTo(knftables.NATType),
		Hook:     knftables.PtrTo(knftables.PostroutingHook),
		Priority: knftables.PtrTo(knftables.SNATPriority),
	})
	tx.Flush(&knftables.Chain{Name: "postrouting"})
	for _, rule := range []string{
		knftables.Concat("ip saddr", n.gateway, "return"),
		knftables.Concat("ip daddr", multicastSubnetIPv4, "return"),
		knftables.Concat("ip saddr", n.subnet, "ip daddr !=", n.subnet, "masquerade"),
	} {
		tx.Add(&knftables.Rule{Chain: "postrouting", Rule: rule, Comment: knftables.PtrTo(n.comment())})
	}
	return nft.Run(ctx, tx)
}

// deleteStaleOwnRules deletes rules in chain that carry this network's comment
// but are not in want, e.g. after the subnet changed.
func deleteStaleOwnRules(ipt iptablesClient, table, chain string, n *subnetNetwork, want [][]string) error {
	rules, err := listRules(ipt, table, chain)
	if err != nil {
		return err
	}
	var stale []int
	for i, rule := range rules {
		if ruleComment(rule) != n.comment() {
			continue
		}
		if !slices.ContainsFunc(want, func(w []string) bool { return slices.Equal(rule, w) }) {
			stale = append(stale, i+1)
		}
	}
	return deleteRulesByID(ipt, table, chain, stale)
}

// cleanupPerContainerIPTablesRules removes rules older engines added per
// container through the bridge plugin's ipMasq and the firewall plugin. Deleting
// their network namespaces with the current CNI config no longer removes them.
func cleanupPerContainerIPTablesRules(ipt iptablesClient, n *subnetNetwork) error {
	var errs []error

	// Bridge ipMasq: POSTROUTING jumps to a per-container chain, commented
	// `name: "<network>" id: "<container>"`.
	if rules, err := listRules(ipt, "nat", "POSTROUTING"); err != nil {
		errs = append(errs, err)
	} else {
		prefix := fmt.Sprintf("name: %q id: ", n.name)
		var ids []int
		var chains []string
		for i, rule := range rules {
			if !strings.HasPrefix(ruleComment(rule), prefix) {
				continue
			}
			ids = append(ids, i+1)
			if target := ruleTarget(rule); strings.HasPrefix(target, "CNI-") {
				chains = append(chains, target)
			}
		}
		if err := deleteRulesByID(ipt, "nat", "POSTROUTING", ids); err != nil {
			errs = append(errs, err)
		}
		for _, chain := range chains {
			if err := ipt.ClearAndDeleteChain("nat", chain); err != nil {
				errs = append(errs, err)
			}
		}
	}

	// Firewall plugin: uncommented per-IP accepts in CNI-FORWARD.
	exists, err := ipt.ChainExists("filter", cniForwardChain)
	if err != nil {
		errs = append(errs, err)
	} else if exists {
		rules, err := listRules(ipt, "filter", cniForwardChain)
		if err != nil {
			errs = append(errs, err)
		} else {
			var ids []int
			for i, rule := range rules {
				if isFirewallPluginRule(rule, n.subnet) {
					ids = append(ids, i+1)
				}
			}
			if err := deleteRulesByID(ipt, "filter", cniForwardChain, ids); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// isFirewallPluginRule reports whether rule is one of the CNI firewall
// plugin's per-IP rules for an address in subnet:
//
//	-d IP/32 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
//	-s IP/32 -j ACCEPT
func isFirewallPluginRule(rule []string, subnet *net.IPNet) bool {
	var addr string
	switch {
	case len(rule) == 4 && rule[0] == "-s" && rule[2] == "-j" && rule[3] == "ACCEPT":
		addr = rule[1]
	case len(rule) == 8 && rule[0] == "-d" && slices.Equal(rule[2:], []string{"-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"}):
		addr = rule[1]
	default:
		return false
	}
	ip, ipNet, err := net.ParseCIDR(addr)
	if err != nil {
		return false
	}
	if ones, bits := ipNet.Mask.Size(); ones != bits {
		return false
	}
	return subnet.Contains(ip)
}

// cleanupPerContainerNFTRules removes per-container rules the bridge plugin's
// nftables masquerade added for this network. Their comments start with a
// hash of the network name.
func cleanupPerContainerNFTRules(ctx context.Context, nft knftables.Interface, n *subnetNetwork) error {
	rules, err := nft.ListRules(ctx, cniNFTMasqChain)
	if err != nil {
		if knftables.IsNotFound(err) {
			return nil
		}
		return err
	}
	prefix := sha512Hex(n.name, 16) + "-"
	tx := nft.NewTransaction()
	for _, rule := range rules {
		if rule.Comment != nil && strings.HasPrefix(*rule.Comment, prefix) {
			tx.Delete(rule)
		}
	}
	if tx.NumOperations() == 0 {
		return nil
	}
	return nft.Run(ctx, tx)
}

// listRules returns the rules of chain as argument lists, in order, without
// the leading "-A <chain>".
func listRules(ipt iptablesClient, table, chain string) ([][]string, error) {
	lines, err := ipt.List(table, chain)
	if err != nil {
		return nil, err
	}
	var rules [][]string
	for _, line := range lines {
		args, err := splitRule(line)
		if err != nil {
			return nil, fmt.Errorf("parse %s rule %q: %w", chain, line, err)
		}
		if len(args) < 2 || args[0] != "-A" {
			continue
		}
		rules = append(rules, args[2:])
	}
	return rules, nil
}

// deleteRulesByID deletes rules by their 1-based position, last first so
// earlier positions stay valid.
func deleteRulesByID(ipt iptablesClient, table, chain string, ids []int) error {
	slices.Sort(ids)
	for _, id := range slices.Backward(ids) {
		if err := ipt.DeleteById(table, chain, id); err != nil {
			return err
		}
	}
	return nil
}

// splitRule splits a line of `iptables -S` output into arguments. iptables
// double-quotes arguments with spaces and backslash-escapes quotes in them.
func splitRule(line string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inArg, quoted := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quoted && c == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
		case c == '"':
			quoted = !quoted
			inArg = true
		case !quoted && c == ' ':
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteByte(c)
			inArg = true
		}
	}
	if quoted {
		return nil, errors.New("unterminated quote")
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}

func ruleComment(rule []string) string {
	for i := 0; i+1 < len(rule); i++ {
		if rule[i] == "--comment" {
			return rule[i+1]
		}
	}
	return ""
}

func ruleTarget(rule []string) string {
	for i := 0; i+1 < len(rule); i++ {
		if rule[i] == "-j" {
			return rule[i+1]
		}
	}
	return ""
}
