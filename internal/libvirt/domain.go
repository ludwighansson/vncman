// Package libvirt lists the VMs on a node by running read-only virsh over
// SSH and parsing the domain XML (ADR 0001).
package libvirt

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Domain is a running libvirt domain.
type Domain struct {
	Name string // libvirt name
	UUID string
	Nova *Nova // nil unless the domain is a Nova instance
	VNC  *VNC  // nil unless the domain has a VNC server on a TCP port

	// Graphics describes the domain's graphics when VNC is nil, for
	// example "spice" or "vnc on a UNIX socket".
	Graphics string
}

// Nova holds the OpenStack metadata of a Nova instance.
type Nova struct {
	Name    string
	Project string
	Flavor  string
}

// VNC is where the domain's VNC server listens, as seen from the node.
type VNC struct {
	Host string
	Port int
}

// DisplayName is the Nova instance name if there is one, otherwise the
// libvirt name.
func (d Domain) DisplayName() string {
	if d.Nova != nil && d.Nova.Name != "" {
		return d.Nova.Name
	}
	return d.Name
}

// Project returns the Nova project name, or "".
func (d Domain) Project() string {
	if d.Nova == nil {
		return ""
	}
	return d.Nova.Project
}

// Flavor returns the Nova flavor name, or "".
func (d Domain) Flavor() string {
	if d.Nova == nil {
		return ""
	}
	return d.Nova.Flavor
}

const novaNamespacePrefix = "http://openstack.org/xmlns/libvirt/nova/"

type xmlDomain struct {
	XMLName  xml.Name `xml:"domain"`
	Name     string   `xml:"name"`
	UUID     string   `xml:"uuid"`
	Metadata struct {
		Instances []xmlNova `xml:"instance"`
	} `xml:"metadata"`
	Graphics []xmlGraphics `xml:"devices>graphics"`
}

type xmlNova struct {
	XMLName xml.Name
	Name    string `xml:"name"`
	Flavor  struct {
		Name string `xml:"name,attr"`
	} `xml:"flavor"`
	Project string `xml:"owner>project"`
}

type xmlGraphics struct {
	Type    string `xml:"type,attr"`
	Port    int    `xml:"port,attr"`
	Listen  string `xml:"listen,attr"`
	Socket  string `xml:"socket,attr"`
	Listens []struct {
		Type    string `xml:"type,attr"`
		Address string `xml:"address,attr"`
	} `xml:"listen"`
}

// ParseDomains parses a stream of <domain> documents, as printed by one or
// more "virsh dumpxml" commands.
func ParseDomains(r io.Reader) ([]Domain, error) {
	dec := xml.NewDecoder(r)
	var domains []Domain
	for {
		var x xmlDomain
		err := dec.Decode(&x)
		if errors.Is(err, io.EOF) {
			return domains, nil
		}
		if err != nil {
			return nil, fmt.Errorf("parsing domain XML: %w", err)
		}
		domains = append(domains, x.domain())
	}
}

func (x xmlDomain) domain() Domain {
	d := Domain{Name: x.Name, UUID: strings.ToLower(strings.TrimSpace(x.UUID))}
	for _, n := range x.Metadata.Instances {
		if strings.HasPrefix(n.XMLName.Space, novaNamespacePrefix) {
			d.Nova = &Nova{
				Name:    strings.TrimSpace(n.Name),
				Project: strings.TrimSpace(n.Project),
				Flavor:  n.Flavor.Name,
			}
			break
		}
	}
	d.VNC, d.Graphics = pickVNC(x.Graphics)
	return d
}

func pickVNC(graphics []xmlGraphics) (*VNC, string) {
	var other []string
	for _, g := range graphics {
		if g.Type != "vnc" {
			other = append(other, g.Type)
			continue
		}
		addr, socket, none := g.Listen, g.Socket != "", false
		for _, l := range g.Listens {
			switch l.Type {
			case "socket":
				socket = true
			case "none":
				none = true
			case "address", "network":
				if addr == "" {
					addr = l.Address
				}
			}
		}
		switch {
		case socket:
			other = append(other, "vnc on a UNIX socket")
		case none:
			other = append(other, "vnc without a listener")
		case g.Port <= 0:
			other = append(other, "vnc without a port")
		default:
			return &VNC{Host: connectHost(addr), Port: g.Port}, ""
		}
	}
	if len(other) == 0 {
		return nil, "no graphics"
	}
	return nil, strings.Join(other, ", ")
}

// connectHost turns a listen address into an address to connect to from
// the node. A wildcard address also accepts connections on loopback.
func connectHost(listen string) string {
	switch strings.Trim(listen, "[]") {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	}
	return strings.Trim(listen, "[]")
}

// SortDomains sorts domains by display name, then UUID.
func SortDomains(domains []Domain) {
	sort.SliceStable(domains, func(i, j int) bool {
		a, b := strings.ToLower(domains[i].DisplayName()), strings.ToLower(domains[j].DisplayName())
		if a != b {
			return a < b
		}
		return domains[i].UUID < domains[j].UUID
	})
}
