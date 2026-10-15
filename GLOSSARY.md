# tnl

Exposes services on machines behind NAT at public hostnames, through a public server the operator runs themselves.

## Language

**Edge**:
The public server that holds the public address, terminates HTTPS for every Hostname, and accepts Tunnels from Connectors.
_Avoid_: Server, relay, gateway

**Connector**:
The tnl process on a machine behind NAT that dials out to the Edge and forwards traffic to Services on its side. It is known by a name the operator gives it when adding it, which never changes.
_Avoid_: Agent, client, origin, site

**Join token**:
A one-time, expiring secret that lets one newly added Connector join the Edge under the name it was issued for.
_Avoid_: Enrollment key, auth key, provisioning key, invite

**Tunnel**:
The encrypted link between one Connector and the Edge.
_Avoid_: Using "tunnel" for a named group of Connectors

**Route**:
A mapping from one Hostname to one Service reached through one Connector.
_Avoid_: Ingress rule, mapping, resource

**Hostname**:
The public name a Route answers on, such as `app.example.com`.
_Avoid_: Domain, subdomain, public hostname

**Zone**:
A domain the Edge serves Hostnames under, with its own certificate mode: one certificate per Hostname, or one wildcard certificate obtained through the Zone's DNS provider.
_Avoid_: Domain, base domain, apex

**Service**:
An HTTP or HTTPS endpoint the Connector can reach, either on its own machine or elsewhere on its network.
_Avoid_: Origin, backend, upstream, target
