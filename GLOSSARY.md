# tnl

Exposes services on machines behind NAT at public hostnames, through a public server the operator runs themselves.

## Language

**Edge**:
The public server that holds the public address, terminates HTTPS for every Hostname, and accepts Tunnels from Outposts.
_Avoid_: Server, relay, gateway

**Outpost**:
The tnl process on a machine behind NAT that dials out to the Edge and forwards traffic to Services on its side. It is known by a name the operator gives it when adding it, which never changes.
_Avoid_: Connector, node, agent, client, origin, site

**Join token**:
A one-time, expiring secret that lets one newly added Outpost join the Edge under the name it was issued for.
_Avoid_: Enrollment key, auth key, provisioning key, invite

**Outpost credential**:
A durable secret that is an Outpost's identity: its private key, plus what it needs to reach and trust the Edge. Anyone holding it can connect as that Outpost until it's replaced.
_Avoid_: Tunnel token, enrollment key, auth key, join token

**Offline**:
An Outpost the Edge has stopped hearing from, so its Routes can't be served until it's heard from again.
_Avoid_: Down, disconnected, dead

**Incompatible**:
An Outpost whose version the Edge doesn't accept. It stays connected, but its Routes aren't served until one side is upgraded.
_Avoid_: Outdated, unsupported

**Tunnel**:
The encrypted link between one Outpost and the Edge.
_Avoid_: Using "tunnel" for a named group of Outposts

**Route**:
A mapping from one Hostname to one Service reached through one Outpost.
_Avoid_: Ingress rule, mapping, resource

**Declaration**:
The whole list of Routes an Outpost asks for in its own settings. While an Outpost has one, its Routes change only through it: a Route left out is removed, and one whose Hostname is taken or outside every Zone waits until it can become a Route.
_Avoid_: Config, manifest, desired state, route file

**Hostname**:
The public name a Route answers on, such as `app.example.com`.
_Avoid_: Domain, subdomain, public hostname

**Zone**:
A domain the Edge serves Hostnames under, with its own certificate mode: one certificate per Hostname, or one wildcard certificate obtained through the Zone's DNS provider, covering the Zone's own name and every name one label below it. Zones may nest, and a Hostname belongs to the longest Zone that contains it.
_Avoid_: Domain, base domain, apex, DNS zone

**DNS provider**:
The service that hosts a Zone's DNS records, which the Edge writes to in order to get the Zone's wildcard certificate. The provider's own zone may be a parent of the Zone: the Zone `tnl.example.com` can live in the provider's `example.com`.
_Avoid_: DNS host, registrar

**Service**:
An HTTP or HTTPS endpoint the Outpost can reach, either on its own machine or elsewhere on its network.
_Avoid_: Origin, backend, upstream, target

**Visitor**:
Anyone who reaches a Hostname through the Edge.
_Avoid_: User, client, end user
