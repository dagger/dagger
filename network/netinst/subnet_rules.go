package netinst

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"slices"
	"strings"

	"github.com/coreos/go-iptables/iptables"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"sigs.k8s.io/knftables"
)

// The engine's CNI config has no per-container NAT rules (no bridge ipMasq)
// and, unless firewalld manages the network, no firewall plugin.
// InstallSubnetRules installs the equivalent rules once for the whole
// container subnet instead.
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

	// The CNI bridge plugin's nftables masquerade table, reused on nftables
	// hosts so rules administrators added to masq_checks keep applying.
	cniNFTMasqTable        = "cni_plugins_masquerade"
	cniNFTMasqChain        = "masq_checks"
	cniNFTMasqTableComment = "Masquerading for plugins from github.com/containernetworking/plugins"
	cniNFTMasqChainComment = "Masquerade traffic from certain IPs to any (non-multicast) IP outside their subnet"
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
	DeleteIfExists(table, chain string, rulespec ...string) error
}

// nftRuleSubnets returns, by rule handle, the destination subnet each rule in
// the bridge plugin's masq_checks chain excludes from masquerading.
type nftRuleSubnets func(ctx context.Context) (map[int]*net.IPNet, error)

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
	return "DAGGER-MASQ-" + sha512Hex(n.name)
}

// sha512Hex returns the first 16 hex digits of the SHA-512 of s, the hashing
// the CNI plugins use for chain names and rule comments.
func sha512Hex(s string) string {
	sum := sha512.Sum512([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
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

// nftMasqRule is this network's rule in the bridge plugin's masq_checks chain,
// which postrouting reaches after returning for multicast.
func (n *subnetNetwork) nftMasqRule() string {
	return knftables.Concat("ip saddr", n.subnet, "ip saddr !=", n.gateway, "ip daddr !=", n.subnet, "masquerade")
}

// InstallSubnetRules installs the masquerade rules for the engine's container
// network and, if forward is set, its forwarding rules. forward is unset when
// the CNI firewall plugin is kept because firewalld manages the network; then
// forwarding rules this network installed before are removed.
// It also removes per-container rules left by older engines in the same
// network namespace. It is idempotent.
//
// Forwarding rules always go through the iptables command, as the CNI firewall
// plugin's did; EnsureIptablesSymlinks points it at the legacy or nft variant.
// Masquerading uses the same backend the bridge plugin would have used.
func InstallSubnetRules(ctx context.Context, name, cidr string, forward bool) error {
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
	cniNFT, err := knftables.New(knftables.InetFamily, cniNFTMasqTable)
	if err != nil {
		if backend == "nftables" {
			return fmt.Errorf("nftables: %w", err)
		}
		// Only needed to clean up after older engines on nftables hosts.
		bklog.G(ctx).WithError(err).Debug("nftables unavailable; skipping leftover nftables masquerade cleanup")
		cniNFT = nil
	}
	if err := installSubnetRules(ctx, n, ipt, cniNFT, listNFTMasqSubnets, backend == "nftables", forward); err != nil {
		return err
	}
	bklog.G(ctx).Infof("installed subnet rules for %s (%s)", n.name, n.subnet)
	return nil
}

// installSubnetRules masquerades in cniNFT if nftMasq is set, else with
// iptables. cniNFT, if set, is also cleaned of the bridge plugin's
// per-container rules for this network.
func installSubnetRules(ctx context.Context, n *subnetNetwork, ipt iptablesClient, cniNFT knftables.Interface, nftSubnets nftRuleSubnets, nftMasq, forward bool) error {
	cleanupLeftovers(ctx, n, ipt, cniNFT, nftSubnets)

	if forward {
		if err := ensureForwardRules(ipt, n); err != nil {
			return fmt.Errorf("install forward rules: %w", err)
		}
	} else if err := removeForwardRules(ipt, n); err != nil {
		return fmt.Errorf("remove forward rules: %w", err)
	}
	if nftMasq {
		if err := ensureNFTMasquerade(ctx, cniNFT, n); err != nil {
			return fmt.Errorf("install nftables masquerade: %w", err)
		}
		return nil
	}
	if err := ensureIPTablesMasquerade(ipt, n); err != nil {
		return fmt.Errorf("install iptables masquerade: %w", err)
	}
	return nil
}

// cleanupLeftovers removes per-container rules older engines added for this
// network: the bridge plugin's masquerade rules and the firewall plugin's
// per-IP accepts. Deleting their network namespaces with the current CNI
// config no longer removes them. Failures only warn.
//
// The firewall plugin's rules carry no owner, so they are found by address,
// in the current subnet or in subnets the masquerade leftovers show the
// network used before. Those masquerade rules are removed only once the
// firewall rules are, so a failed run can be retried on the next start.
func cleanupLeftovers(ctx context.Context, n *subnetNetwork, ipt iptablesClient, cniNFT knftables.Interface, nftSubnets nftRuleSubnets) {
	l, readErr := findLeftovers(ctx, n, ipt, cniNFT, nftSubnets)
	if readErr != nil {
		bklog.G(ctx).WithError(readErr).Warn("failed to read leftover per-container masquerade rules; keeping them for the next start")
	}
	if err := cleanupFirewallPluginRules(ipt, append([]*net.IPNet{n.subnet}, l.subnets...)); err != nil {
		bklog.G(ctx).WithError(err).Warn("failed to remove leftover per-container firewall rules; keeping masquerade leftovers for the next start")
		return
	}
	if readErr != nil {
		return
	}
	if err := l.remove(ctx, ipt, cniNFT); err != nil {
		bklog.G(ctx).WithError(err).Warn("failed to remove leftover per-container masquerade rules")
	}
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

// removeForwardRules removes this network's forward rules, for when the CNI
// firewall plugin manages forwarding instead. The shared CNI chains and any
// administrator rules stay.
func removeForwardRules(ipt iptablesClient, n *subnetNetwork) error {
	exists, err := ipt.ChainExists("filter", cniForwardChain)
	if err != nil || !exists {
		return err
	}
	return deleteStaleOwnRules(ipt, "filter", cniForwardChain, n, nil)
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

// ensureNFTMasquerade puts this network's rule in the bridge plugin's
// masq_checks chain, set up the way the plugin sets it up. An existing rule of
// ours is replaced in place, so rules around it keep their order; otherwise
// the rule is appended, as the plugin appended per-container rules.
func ensureNFTMasquerade(ctx context.Context, nft knftables.Interface, n *subnetNetwork) error {
	var own []*knftables.Rule
	rules, err := nft.ListRules(ctx, cniNFTMasqChain)
	if err != nil && !knftables.IsNotFound(err) {
		return err
	}
	for _, rule := range rules {
		if rule.Comment != nil && *rule.Comment == n.comment() {
			own = append(own, rule)
		}
	}

	tx := nft.NewTransaction()
	tx.Add(&knftables.Table{Comment: knftables.PtrTo(cniNFTMasqTableComment)})
	tx.Add(&knftables.Chain{Name: cniNFTMasqChain, Comment: knftables.PtrTo(cniNFTMasqChainComment)})
	tx.Add(&knftables.Chain{
		Name:     "postrouting",
		Type:     knftables.PtrTo(knftables.NATType),
		Hook:     knftables.PtrTo(knftables.PostroutingHook),
		Priority: knftables.PtrTo(knftables.SNATPriority),
	})
	tx.Flush(&knftables.Chain{Name: "postrouting"})
	tx.Add(&knftables.Rule{Chain: "postrouting", Rule: "ip daddr == 224.0.0.0/4  return"})
	tx.Add(&knftables.Rule{Chain: "postrouting", Rule: "ip6 daddr == ff00::/8  return"})
	tx.Add(&knftables.Rule{Chain: "postrouting", Rule: knftables.Concat("goto", cniNFTMasqChain)})

	rule := &knftables.Rule{Chain: cniNFTMasqChain, Rule: n.nftMasqRule(), Comment: knftables.PtrTo(n.comment())}
	if len(own) == 0 {
		tx.Add(rule)
	} else {
		rule.Handle = own[0].Handle
		tx.Replace(rule)
		for _, extra := range own[1:] {
			tx.Delete(extra)
		}
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
	var stale [][]string
	for _, rule := range rules {
		if ruleComment(rule) != n.comment() {
			continue
		}
		if !slices.ContainsFunc(want, func(w []string) bool { return slices.Equal(rule, w) }) {
			stale = append(stale, rule)
		}
	}
	return deleteRules(ipt, table, chain, stale)
}

// leftovers are per-container masquerade rules older engines added for this
// network, and the subnets they covered.
type leftovers struct {
	iptRules  [][]string // jumps in nat POSTROUTING
	iptChains []string   // the per-container chains they jump to
	nftRules  []*knftables.Rule
	subnets   []*net.IPNet
}

// findLeftovers finds, without changing anything, the rules the bridge
// plugin's ipMasq added for this network's containers: POSTROUTING jumps to
// per-container chains commented `name: "<network>" id: "<container>"`, and,
// in cniNFT, masq_checks rules whose comments start with a hash of the
// network name. The subnet each covered comes from the chain's
// `-d <subnet> ... -j ACCEPT` rule, or from the nftables rule's excluded
// destination.
func findLeftovers(ctx context.Context, n *subnetNetwork, ipt iptablesClient, cniNFT knftables.Interface, nftSubnets nftRuleSubnets) (*leftovers, error) {
	l := &leftovers{}
	var errs []error

	rules, err := listRules(ipt, "nat", "POSTROUTING")
	if err != nil {
		errs = append(errs, err)
	}
	prefix := fmt.Sprintf("name: %q id: ", n.name)
	for _, rule := range rules {
		if !strings.HasPrefix(ruleComment(rule), prefix) {
			continue
		}
		l.iptRules = append(l.iptRules, rule)
		target := ruleTarget(rule)
		if !strings.HasPrefix(target, "CNI-") {
			continue
		}
		l.iptChains = append(l.iptChains, target)
		chainRules, err := listRules(ipt, "nat", target)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, rule := range chainRules {
			if len(rule) > 1 && rule[0] == "-d" && ruleTarget(rule) == "ACCEPT" {
				if _, subnet, err := net.ParseCIDR(rule[1]); err == nil {
					l.subnets = append(l.subnets, subnet)
				}
			}
		}
	}

	if cniNFT != nil {
		nftRules, err := cniNFT.ListRules(ctx, cniNFTMasqChain)
		if err != nil && !knftables.IsNotFound(err) {
			errs = append(errs, err)
		}
		hash := sha512Hex(n.name) + "-"
		for _, rule := range nftRules {
			if rule.Comment != nil && strings.HasPrefix(*rule.Comment, hash) {
				l.nftRules = append(l.nftRules, rule)
			}
		}
		if len(l.nftRules) > 0 {
			bySubnet, err := nftSubnets(ctx)
			if err != nil {
				errs = append(errs, fmt.Errorf("read nftables masquerade subnets: %w", err))
			}
			for _, rule := range l.nftRules {
				if rule.Handle != nil && bySubnet[*rule.Handle] != nil {
					l.subnets = append(l.subnets, bySubnet[*rule.Handle])
				}
			}
		}
	}
	return l, errors.Join(errs...)
}

func (l *leftovers) remove(ctx context.Context, ipt iptablesClient, cniNFT knftables.Interface) error {
	var errs []error
	if err := deleteRules(ipt, "nat", "POSTROUTING", l.iptRules); err != nil {
		errs = append(errs, err)
	}
	for _, chain := range l.iptChains {
		if err := ipt.ClearAndDeleteChain("nat", chain); err != nil {
			errs = append(errs, err)
		}
	}
	if len(l.nftRules) > 0 {
		tx := cniNFT.NewTransaction()
		for _, rule := range l.nftRules {
			tx.Delete(rule)
		}
		if err := cniNFT.Run(ctx, tx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// listNFTMasqSubnets reads the bridge plugin's masq_checks rules, which look
// like `ip saddr <container IP> ip daddr != <subnet> masquerade`. knftables
// lists only handles and comments, so this reads nft's JSON output.
func listNFTMasqSubnets(ctx context.Context) (map[int]*net.IPNet, error) {
	out, err := exec.CommandContext(ctx, "nft", "--json", "list", "chain", string(knftables.InetFamily), cniNFTMasqTable, cniNFTMasqChain).Output()
	if err != nil {
		return nil, err
	}
	return parseNFTMasqSubnets(out)
}

func parseNFTMasqSubnets(out []byte) (map[int]*net.IPNet, error) {
	var doc struct {
		Nftables []struct {
			Rule *struct {
				Handle int `json:"handle"`
				Expr   []struct {
					Match *struct {
						Op   string `json:"op"`
						Left struct {
							Payload *struct {
								Field string `json:"field"`
							} `json:"payload"`
						} `json:"left"`
						Right json.RawMessage `json:"right"`
					} `json:"match"`
				} `json:"expr"`
			} `json:"rule"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, err
	}
	subnets := map[int]*net.IPNet{}
	for _, item := range doc.Nftables {
		if item.Rule == nil {
			continue
		}
		for _, expr := range item.Rule.Expr {
			m := expr.Match
			if m == nil || m.Op != "!=" || m.Left.Payload == nil || m.Left.Payload.Field != "daddr" {
				continue
			}
			var right struct {
				Prefix *struct {
					Addr string `json:"addr"`
					Len  int    `json:"len"`
				} `json:"prefix"`
			}
			if json.Unmarshal(m.Right, &right) != nil || right.Prefix == nil {
				continue
			}
			if _, subnet, err := net.ParseCIDR(fmt.Sprintf("%s/%d", right.Prefix.Addr, right.Prefix.Len)); err == nil {
				subnets[item.Rule.Handle] = subnet
			}
		}
	}
	return subnets, nil
}

// cleanupFirewallPluginRules removes the CNI firewall plugin's uncommented
// per-IP accepts in CNI-FORWARD for addresses in subnets.
func cleanupFirewallPluginRules(ipt iptablesClient, subnets []*net.IPNet) error {
	exists, err := ipt.ChainExists("filter", cniForwardChain)
	if err != nil || !exists {
		return err
	}
	rules, err := listRules(ipt, "filter", cniForwardChain)
	if err != nil {
		return err
	}
	var stale [][]string
	for _, rule := range rules {
		ip := firewallPluginRuleIP(rule)
		if ip != nil && slices.ContainsFunc(subnets, func(s *net.IPNet) bool { return s.Contains(ip) }) {
			stale = append(stale, rule)
		}
	}
	return deleteRules(ipt, "filter", cniForwardChain, stale)
}

// firewallPluginRuleIP returns the address of one of the CNI firewall
// plugin's per-IP rules, or nil if rule is not one:
//
//	-d IP/32 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
//	-s IP/32 -j ACCEPT
func firewallPluginRuleIP(rule []string) net.IP {
	var addr string
	switch {
	case len(rule) == 4 && rule[0] == "-s" && rule[2] == "-j" && rule[3] == "ACCEPT":
		addr = rule[1]
	case len(rule) == 8 && rule[0] == "-d" && slices.Equal(rule[2:], []string{"-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"}):
		addr = rule[1]
	default:
		return nil
	}
	ip, ipNet, err := net.ParseCIDR(addr)
	if err != nil {
		return nil
	}
	if ones, bits := ipNet.Mask.Size(); ones != bits {
		return nil
	}
	return ip
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

// deleteRules deletes rules, as listed, by their specification. Positions
// from the listing could point at other rules by now: in a shared network
// namespace, other CNI plugins or administrators may change the chain between
// the listing and the deletion. A rule that is already gone is skipped.
func deleteRules(ipt iptablesClient, table, chain string, rules [][]string) error {
	for _, rule := range rules {
		if err := ipt.DeleteIfExists(table, chain, rule...); err != nil {
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
