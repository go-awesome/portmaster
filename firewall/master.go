package firewall

import (
	"net"
	"os"
	"strings"

	"github.com/Safing/portbase/log"
	"github.com/Safing/portmaster/intel"
	"github.com/Safing/portmaster/network"
	"github.com/Safing/portmaster/network/netutils"
	"github.com/Safing/portmaster/network/packet"

	"github.com/agext/levenshtein"
)

// Call order:
//
// 1. DecideOnConnectionBeforeIntel (if connecting to domain)
//    is called when a DNS query is made, before the query is resolved
// 2. DecideOnConnectionAfterIntel (if connecting to domain)
//    is called when a DNS query is made, after the query is resolved
// 3. DecideOnConnection
//    is called when the first packet of the first link of the connection arrives
// 4. DecideOnLink
//		is called when when the first packet of a link arrives only if connection has verdict UNDECIDED or CANTSAY

func DecideOnConnectionBeforeIntel(connection *network.Connection, fqdn string) {
	// check:
	// Profile.DomainWhitelist
	// Profile.Flags
	// - process specific: System, Admin, User
	// - network specific: Internet, LocalNet

	// grant self
	if connection.Process().Pid == os.Getpid() {
		log.Infof("firewall: granting own connection %s", connection)
		connection.Accept()
		return
	}

	// check if there is a profile
	profileSet := connection.Process().ProfileSetSet
	if profile == nil {
		log.Infof("firewall: no profile, denying connection %s", connection)
		connection.AddReason("no profile")
		connection.Block()
		return
	}

	// check user class
	if profileSet.CheckFlag(profile.System) {
		if !connection.Process().IsSystem() {
			log.Infof("firewall: denying connection %s, profile has System flag set, but process is not executed by System", connection)
			connection.AddReason("must be executed by system")
			connection.Block()
			return
		}
	}
	if profileSet.CheckFlag(profile.Admin) {
		if !connection.Process().IsAdmin() {
			log.Infof("firewall: denying connection %s, profile has Admin flag set, but process is not executed by Admin", connection)
			connection.AddReason("must be executed by admin")
			connection.Block()
			return
		}
	}
	if profileSet.CheckFlag(profile.User) {
		if !connection.Process().IsUser() {
			log.Infof("firewall: denying connection %s, profile has User flag set, but process is not executed by a User", connection)
			connection.AddReason("must be executed by user")
			connection.Block()
			return
		}
	}

	// check for any network access
	if !profileSet.CheckFlag(profile.Internet) && !profileSet.CheckFlag(profile.LocalNet) {
		log.Infof("firewall: denying connection %s, profile denies Internet and local network access", connection)
		connection.Block()
		return
	}

	// check domain whitelist/blacklist
	if len(profile.DomainWhitelist) > 0 {
		matched := false
		for _, entry := range profile.DomainWhitelist {
			if !strings.HasSuffix(entry, ".") {
				entry += "."
			}
			if strings.HasPrefix(entry, "*") {
				if strings.HasSuffix(fqdn, strings.Trim(entry, "*")) {
					matched = true
					break
				}
			} else {
				if entry == fqdn {
					matched = true
					break
				}
			}
		}
		if matched {
			if profile.DomainWhitelistIsBlacklist {
				log.Infof("firewall: denying connection %s, profile has %s in domain blacklist", connection, fqdn)
				connection.AddReason("domain blacklisted")
				connection.Block()
				return
			}
		} else {
			if !profile.DomainWhitelistIsBlacklist {
				log.Infof("firewall: denying connection %s, profile does not have %s in domain whitelist", connection, fqdn)
				connection.AddReason("domain not in whitelist")
				connection.Block()
				return
			}
		}
	}

}

func DecideOnConnectionAfterIntel(connection *network.Connection, fqdn string, rrCache *intel.RRCache) *intel.RRCache {
	// check:
	// TODO: Profile.ClassificationBlacklist
	// TODO: Profile.ClassificationWhitelist
	// Profile.Flags
	// - network specific: Strict

	// check if there is a profile
	profileSet := connection.Process().ProfileSet
	// FIXME: there should always be a profile
	if profileSet == nil {
		log.Infof("firewall: no profile, denying connection %s", connection)
		connection.AddReason("no profile")
		connection.Block()
		return rrCache
	}

	// check Strict flag
	// TODO: drastically improve this!
	if profileSet.CheckFlag(profile.Related) {
		matched := false
		pathElements := strings.Split(connection.Process().Path, "/")
		if len(pathElements) > 2 {
			pathElements = pathElements[len(pathElements)-2:]
		}
		domainElements := strings.Split(fqdn, ".")
	matchLoop:
		for _, domainElement := range domainElements {
			for _, pathElement := range pathElements {
				if levenshtein.Match(domainElement, pathElement, nil) > 0.5 {
					matched = true
					break matchLoop
				}
			}
			if levenshtein.Match(domainElement, profile.Name, nil) > 0.5 {
				matched = true
				break matchLoop
			}
			if levenshtein.Match(domainElement, connection.Process().Name, nil) > 0.5 {
				matched = true
				break matchLoop
			}
		}
		if !matched {
			log.Infof("firewall: denying connection %s, profile has declared Strict flag and no match to domain was found", connection)
			connection.AddReason("domain does not relate to process")
			connection.Block()
			return rrCache
		}
	}

	// tunneling
	// TODO: link this to real status
	// gate17Active := mode.Client()
	// if gate17Active {
	// 	tunnelInfo, err := AssignTunnelIP(fqdn)
	// 	if err != nil {
	// 		log.Errorf("portmaster: could not get tunnel IP for routing %s: %s", connection, err)
	// 		return nil // return nxDomain
	// 	}
	// 	// save original reply
	// 	tunnelInfo.RRCache = rrCache
	// 	// return tunnel IP
	// 	return tunnelInfo.ExportTunnelIP()
	// }

	return rrCache
}

func DecideOnConnection(connection *network.Connection, pkt packet.Packet) {
	// check:
	// Profile.Flags
	// - process specific: System, Admin, User
	// - network specific: Internet, LocalNet, Service, Directconnect

	// grant self
	if connection.Process().Pid == os.Getpid() {
		log.Infof("firewall: granting own connection %s", connection)
		connection.Accept()
		return
	}

	// check if there is a profile
	profileSet := connection.Process().ProfileSet
	if profile == nil {
		log.Infof("firewall: no profile, denying connection %s", connection)
		connection.AddReason("no profile")
		connection.Block()
		return
	}

	// check user class
	if profileSet.CheckFlag(profile.System) {
		if !connection.Process().IsSystem() {
			log.Infof("firewall: denying connection %s, profile has System flag set, but process is not executed by System", connection)
			connection.AddReason("must be executed by system")
			connection.Block()
			return
		}
	}
	if profileSet.CheckFlag(profile.Admin) {
		if !connection.Process().IsAdmin() {
			log.Infof("firewall: denying connection %s, profile has Admin flag set, but process is not executed by Admin", connection)
			connection.AddReason("must be executed by admin")
			connection.Block()
			return
		}
	}
	if profileSet.CheckFlag(profile.User) {
		if !connection.Process().IsUser() {
			log.Infof("firewall: denying connection %s, profile has User flag set, but process is not executed by a User", connection)
			connection.AddReason("must be executed by user")
			connection.Block()
			return
		}
	}

	// check for any network access
	if !profileSet.CheckFlag(profile.Internet) && !profileSet.CheckFlag(profile.LocalNet) {
		log.Infof("firewall: denying connection %s, profile denies Internet and local network access", connection)
		connection.AddReason("no network access allowed")
		connection.Block()
		return
	}

	switch connection.Domain {
	case "I":
		// check Service flag
		if !profileSet.CheckFlag(profile.Service) {
			log.Infof("firewall: denying connection %s, profile does not declare service", connection)
			connection.AddReason("not a service")
			connection.Drop()
			return
		}
		// check if incoming connections are allowed on any port, but only if there no other restrictions
		if !!profileSet.CheckFlag(profile.Internet) && !!profileSet.CheckFlag(profile.LocalNet) && len(profile.ListenPorts) == 0 {
			log.Infof("firewall: granting connection %s, profile allows incoming connections from anywhere and on any port", connection)
			connection.Accept()
			return
		}
	case "D":
		// check PeerToPeer flag
		if !profileSet.CheckFlag(profile.PeerToPeer) {
			log.Infof("firewall: denying connection %s, profile does not declare direct connections", connection)
			connection.AddReason("direct connections (without DNS) not allowed")
			connection.Drop()
			return
		}
	}

	log.Infof("firewall: could not decide on connection %s, deciding on per-link basis", connection)
	connection.CantSay()
}

func DecideOnLink(connection *network.Connection, link *network.Link, pkt packet.Packet) {
	// check:
	// Profile.Flags
	// - network specific: Internet, LocalNet
	// Profile.ConnectPorts
	// Profile.ListenPorts

	// check if there is a profile
	profileSet := connection.Process().ProfileSet
	if profile == nil {
		log.Infof("firewall: no profile, denying %s", link)
		link.AddReason("no profile")
		link.UpdateVerdict(network.BLOCK)
		return
	}

	// check LocalNet and Internet flags
	var remoteIP net.IP
	if connection.Direction {
		remoteIP = pkt.GetIPHeader().Src
	} else {
		remoteIP = pkt.GetIPHeader().Dst
	}
	if netutils.IPIsLocal(remoteIP) {
		if !profileSet.CheckFlag(profile.LocalNet) {
			log.Infof("firewall: dropping link %s, profile does not allow communication in the local network", link)
			link.AddReason("profile does not allow access to local network")
			link.UpdateVerdict(network.BLOCK)
			return
		}
	} else {
		if !profileSet.CheckFlag(profile.Internet) {
			log.Infof("firewall: dropping link %s, profile does not allow communication with the Internet", link)
			link.AddReason("profile does not allow access to the Internet")
			link.UpdateVerdict(network.BLOCK)
			return
		}
	}

	// check connect ports
	if connection.Domain != "I" && len(profile.ConnectPorts) > 0 {

		tcpUdpHeader := pkt.GetTCPUDPHeader()
		if tcpUdpHeader == nil {
			log.Infof("firewall: blocking link %s, profile has declared connect port whitelist, but link is not TCP/UDP", link)
			link.AddReason("profile has declared connect port whitelist, but link is not TCP/UDP")
			link.UpdateVerdict(network.BLOCK)
			return
		}

		// packet *should* be outbound, but we could be deciding on an already active connection.
		var remotePort uint16
		if connection.Direction {
			remotePort = tcpUdpHeader.SrcPort
		} else {
			remotePort = tcpUdpHeader.DstPort
		}

		matched := false
		for _, port := range profile.ConnectPorts {
			if remotePort == port {
				matched = true
				break
			}
		}

		if !matched {
			log.Infof("firewall: blocking link %s, remote port %d not in profile connect port whitelist", link, remotePort)
			link.AddReason("destination port not in whitelist")
			link.UpdateVerdict(network.BLOCK)
			return
		}

	}

	// check listen ports
	if connection.Domain == "I" && len(profile.ListenPorts) > 0 {

		tcpUdpHeader := pkt.GetTCPUDPHeader()
		if tcpUdpHeader == nil {
			log.Infof("firewall: dropping link %s, profile has declared listen port whitelist, but link is not TCP/UDP", link)
			link.AddReason("profile has declared listen port whitelist, but link is not TCP/UDP")
			link.UpdateVerdict(network.DROP)
			return
		}

		// packet *should* be inbound, but we could be deciding on an already active connection.
		var localPort uint16
		if connection.Direction {
			localPort = tcpUdpHeader.DstPort
		} else {
			localPort = tcpUdpHeader.SrcPort
		}

		matched := false
		for _, port := range profile.ListenPorts {
			if localPort == port {
				matched = true
				break
			}
		}

		if !matched {
			log.Infof("firewall: blocking link %s, local port %d not in profile listen port whitelist", link, localPort)
			link.AddReason("listen port not in whitelist")
			link.UpdateVerdict(network.BLOCK)
			return
		}

	}

	log.Infof("firewall: accepting link %s", link)
	link.UpdateVerdict(network.ACCEPT)

}
