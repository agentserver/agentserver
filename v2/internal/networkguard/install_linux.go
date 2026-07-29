//go:build linux

package networkguard

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	netlinkHeaderBytes = 16
	netfilterDrop      = uint32(0)
	netfilterAccept    = uint32(1)
	outputChainName    = "agentserver_output"
)

type nftCommand struct {
	label    string
	typeID   uint16
	flags    uint16
	family   uint8
	attrs    []byte
	sequence uint32
}

// Install creates IPv4 and IPv6 output base chains. For each declared UID the
// IPv4 chain accepts only the exact TCP address/port pairs, actively rejects
// every other TCP destination, and drops all remaining traffic. The IPv6 chain
// drops all traffic for those UIDs so an IPv4-only manifest cannot be bypassed
// through a dual-stack or loopback route. Other UIDs are unaffected so the
// init/fixture process can verify sink sensitivity in the same namespace.
func Install(tableName string, policies []UIDPolicy) error {
	normalized, err := normalize(tableName, policies)
	if err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return errors.New("install nftables UID egress policy as container root")
	}
	commands := newTableAndOutputChainCommands(tableName, unix.NFPROTO_IPV4, "IPv4")
	for _, policy := range normalized {
		for _, endpoint := range policy.AllowedEndpoints {
			commands = append(commands, newRuleCommand(
				fmt.Sprintf("allow uid %d to %s:%d", policy.UID, endpoint.Address, endpoint.Port),
				tableName,
				unix.NFPROTO_IPV4,
				allowEndpointExpressions(policy.UID, endpoint),
			))
		}
		commands = append(commands,
			newRuleCommand(
				fmt.Sprintf("reject other uid %d TCP", policy.UID),
				tableName,
				unix.NFPROTO_IPV4,
				append(uidAndTCPExpressions(policy.UID), rejectTCPExpression()),
			),
			newRuleCommand(
				fmt.Sprintf("drop remaining uid %d traffic", policy.UID),
				tableName,
				unix.NFPROTO_IPV4,
				[]nftExpression{metaSKUIDExpression(), compareUIDExpression(policy.UID), verdictExpression(netfilterDrop)},
			),
		)
	}
	commands = append(commands, newTableAndOutputChainCommands(tableName, unix.NFPROTO_IPV6, "IPv6")...)
	for _, policy := range normalized {
		commands = append(commands, newRuleCommand(
			fmt.Sprintf("drop all IPv6 for uid %d", policy.UID),
			tableName,
			unix.NFPROTO_IPV6,
			[]nftExpression{metaSKUIDExpression(), compareUIDExpression(policy.UID), verdictExpression(netfilterDrop)},
		))
	}
	return transact(commands)
}

func newTableAndOutputChainCommands(tableName string, family uint8, familyLabel string) []nftCommand {
	return []nftCommand{
		{
			label:  "create " + familyLabel + " table",
			typeID: unix.NFT_MSG_NEWTABLE,
			flags:  unix.NLM_F_CREATE | unix.NLM_F_EXCL,
			family: family,
			attrs:  nftAttributes(nftString(unix.NFTA_TABLE_NAME, tableName)),
		},
		{
			label:  "create " + familyLabel + " output chain",
			typeID: unix.NFT_MSG_NEWCHAIN,
			flags:  unix.NLM_F_CREATE | unix.NLM_F_EXCL,
			family: family,
			attrs: nftAttributes(
				nftString(unix.NFTA_CHAIN_TABLE, tableName),
				nftString(unix.NFTA_CHAIN_NAME, outputChainName),
				nftString(unix.NFTA_CHAIN_TYPE, "filter"),
				nftNested(unix.NFTA_CHAIN_HOOK,
					nftU32(unix.NFTA_HOOK_HOOKNUM, unix.NF_INET_LOCAL_OUT),
					nftU32(unix.NFTA_HOOK_PRIORITY, 0),
				),
			),
		},
	}
}

func newRuleCommand(label, tableName string, family uint8, expressions []nftExpression) nftCommand {
	encodedExpressions := make([][]byte, len(expressions))
	for index, expression := range expressions {
		encodedExpressions[index] = expression.encode()
	}
	return nftCommand{
		label:  label,
		typeID: unix.NFT_MSG_NEWRULE,
		flags:  unix.NLM_F_CREATE | unix.NLM_F_APPEND,
		family: family,
		attrs: nftAttributes(
			nftString(unix.NFTA_RULE_TABLE, tableName),
			nftString(unix.NFTA_RULE_CHAIN, outputChainName),
			nftNestedBytes(unix.NFTA_RULE_EXPRESSIONS, encodedExpressions...),
		),
	}
}

func allowEndpointExpressions(uid uint32, endpoint Endpoint) []nftExpression {
	ipv4 := endpoint.Address.As4()
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, endpoint.Port)
	return append(uidAndTCPExpressions(uid),
		payloadExpression(unix.NFT_PAYLOAD_NETWORK_HEADER, 16, 4),
		compareBytesExpression(ipv4[:]),
		payloadExpression(unix.NFT_PAYLOAD_TRANSPORT_HEADER, 2, 2),
		compareBytesExpression(port),
		verdictExpression(netfilterAccept),
	)
}

func uidAndTCPExpressions(uid uint32) []nftExpression {
	return []nftExpression{
		metaSKUIDExpression(),
		compareUIDExpression(uid),
		payloadExpression(unix.NFT_PAYLOAD_NETWORK_HEADER, 9, 1),
		compareBytesExpression([]byte{unix.IPPROTO_TCP}),
	}
}

type nftExpression struct {
	name  string
	attrs [][]byte
}

func (expression nftExpression) encode() []byte {
	return nftNested(unix.NFTA_LIST_ELEM,
		nftString(unix.NFTA_EXPR_NAME, expression.name),
		nftNestedBytes(unix.NFTA_EXPR_DATA, expression.attrs...),
	)
}

func metaSKUIDExpression() nftExpression {
	return nftExpression{name: "meta", attrs: [][]byte{
		nftU32(unix.NFTA_META_KEY, unix.NFT_META_SKUID),
		nftU32(unix.NFTA_META_DREG, unix.NFT_REG_1),
	}}
}

func payloadExpression(base, offset, length uint32) nftExpression {
	return nftExpression{name: "payload", attrs: [][]byte{
		nftU32(unix.NFTA_PAYLOAD_DREG, unix.NFT_REG_1),
		nftU32(unix.NFTA_PAYLOAD_BASE, base),
		nftU32(unix.NFTA_PAYLOAD_OFFSET, offset),
		nftU32(unix.NFTA_PAYLOAD_LEN, length),
	}}
}

func compareUIDExpression(uid uint32) nftExpression {
	data := make([]byte, 4)
	binary.NativeEndian.PutUint32(data, uid)
	return compareBytesExpression(data)
}

func compareBytesExpression(value []byte) nftExpression {
	return nftExpression{name: "cmp", attrs: [][]byte{
		nftU32(unix.NFTA_CMP_SREG, unix.NFT_REG_1),
		nftU32(unix.NFTA_CMP_OP, unix.NFT_CMP_EQ),
		nftNested(unix.NFTA_CMP_DATA, nftBytes(unix.NFTA_DATA_VALUE, value)),
	}}
}

func verdictExpression(verdict uint32) nftExpression {
	return nftExpression{name: "immediate", attrs: [][]byte{
		nftU32(unix.NFTA_IMMEDIATE_DREG, unix.NFT_REG_VERDICT),
		nftNested(unix.NFTA_IMMEDIATE_DATA,
			nftNested(unix.NFTA_DATA_VERDICT, nftU32(unix.NFTA_VERDICT_CODE, verdict)),
		),
	}}
}

func rejectTCPExpression() nftExpression {
	return nftExpression{name: "reject", attrs: [][]byte{
		nftU32(unix.NFTA_REJECT_TYPE, unix.NFT_REJECT_TCP_RST),
	}}
}

func transact(commands []nftCommand) error {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_NETFILTER)
	if err != nil {
		return fmt.Errorf("open nftables netlink socket: %w", err)
	}
	defer unix.Close(fd)
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("bind nftables netlink socket: %w", err)
	}
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Sec: 5}); err != nil {
		return fmt.Errorf("bound nftables netlink receive timeout: %w", err)
	}

	sequence := uint32(os.Getpid()) ^ 0xa1200000
	transaction := nftBatchMessage(unix.NFNL_MSG_BATCH_BEGIN, sequence)
	wantedACKs := make(map[uint32]string, len(commands))
	for index := range commands {
		sequence++
		commands[index].sequence = sequence
		wantedACKs[sequence] = commands[index].label
		transaction = append(transaction, nftCommandMessage(commands[index])...)
	}
	sequence++
	transaction = append(transaction, nftBatchMessage(unix.NFNL_MSG_BATCH_END, sequence)...)
	if err := unix.Sendto(fd, transaction, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("send nftables transaction: %w", err)
	}

	buffer := make([]byte, 64*1024)
	for len(wantedACKs) != 0 {
		bytesRead, _, err := unix.Recvfrom(fd, buffer, 0)
		if err != nil {
			return fmt.Errorf("receive nftables acknowledgement (%d pending): %w", len(wantedACKs), err)
		}
		messages, err := syscall.ParseNetlinkMessage(buffer[:bytesRead])
		if err != nil {
			return fmt.Errorf("parse nftables acknowledgement: %w", err)
		}
		for _, message := range messages {
			label, wanted := wantedACKs[message.Header.Seq]
			if !wanted || message.Header.Type != unix.NLMSG_ERROR {
				continue
			}
			if len(message.Data) < 4 {
				return fmt.Errorf("nftables %s acknowledgement is truncated", label)
			}
			code := int32(binary.NativeEndian.Uint32(message.Data[:4]))
			if code != 0 {
				return fmt.Errorf("nftables %s: %w", label, syscall.Errno(-code))
			}
			delete(wantedACKs, message.Header.Seq)
		}
	}
	return nil
}

func nftBatchMessage(messageType int, sequence uint32) []byte {
	payload := nftGenerationHeader(unix.AF_UNSPEC, unix.NFNL_SUBSYS_NFTABLES)
	return netlinkMessage(uint16(messageType), unix.NLM_F_REQUEST, sequence, payload)
}

func nftCommandMessage(command nftCommand) []byte {
	payload := append(nftGenerationHeader(command.family, 0), command.attrs...)
	messageType := uint16(unix.NFNL_SUBSYS_NFTABLES<<8) | command.typeID
	flags := uint16(unix.NLM_F_REQUEST|unix.NLM_F_ACK) | command.flags
	return netlinkMessage(messageType, flags, command.sequence, payload)
}

func nftGenerationHeader(family uint8, resourceID uint16) []byte {
	result := make([]byte, 4)
	result[0] = family
	result[1] = unix.NFNETLINK_V0
	binary.BigEndian.PutUint16(result[2:], resourceID)
	return result
}

func netlinkMessage(messageType, flags uint16, sequence uint32, payload []byte) []byte {
	length := netlinkHeaderBytes + len(payload)
	result := make([]byte, align4(length))
	binary.NativeEndian.PutUint32(result[0:4], uint32(length))
	binary.NativeEndian.PutUint16(result[4:6], messageType)
	binary.NativeEndian.PutUint16(result[6:8], flags)
	binary.NativeEndian.PutUint32(result[8:12], sequence)
	copy(result[netlinkHeaderBytes:], payload)
	return result
}

func nftAttributes(attributes ...[]byte) []byte {
	var result []byte
	for _, attribute := range attributes {
		result = append(result, attribute...)
	}
	return result
}

func nftString(attributeType uint16, value string) []byte {
	return nftBytes(attributeType, append([]byte(value), 0))
}

func nftU32(attributeType uint16, value uint32) []byte {
	data := make([]byte, 4)
	binary.BigEndian.PutUint32(data, value)
	return nftBytes(attributeType, data)
}

func nftNested(attributeType uint16, attributes ...[]byte) []byte {
	return nftNestedBytes(attributeType, attributes...)
}

func nftNestedBytes(attributeType uint16, attributes ...[]byte) []byte {
	return nftBytes(attributeType|unix.NLA_F_NESTED, nftAttributes(attributes...))
}

func nftBytes(attributeType uint16, value []byte) []byte {
	length := 4 + len(value)
	result := make([]byte, align4(length))
	binary.NativeEndian.PutUint16(result[0:2], uint16(length))
	binary.NativeEndian.PutUint16(result[2:4], attributeType)
	copy(result[4:], value)
	return result
}

func align4(value int) int {
	return (value + 3) &^ 3
}
