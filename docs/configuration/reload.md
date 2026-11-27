---
icon: material/new-box
---

# Reload

`pasadizo run` reads its configuration again when it gets `SIGHUP` (`systemctl reload`, when the unit has `ExecReload=/bin/kill -HUP $MAINPID`). The new configuration is checked first; when it is not valid, the running instance is left alone.

### Rules are replaced in place

When the new configuration differs from the running one **only in `route.rules` and `dns.rules`**, the rules are replaced in the running instance:

* connections that are open stay open, on the outbound they were routed to;
* new connections and DNS queries use the new rules;
* the DNS cache is cleared, because an answer cached under the old rules may come from a server the new rules no longer choose for that name.

The log says `reload: replaced the route and DNS rules, connections are kept`.

### Everything else restarts

Any other change, such as an outbound, a member of a group, an inbound, a DNS server, a rule-set or the log level, restarts the instance as before, which closes every connection.

A change of rules also restarts the instance in these cases, and the log names the reason:

| Case | Reason |
|------|--------|
| The new rules use a rule-set that no rule used before | A rule-set that no rule uses is emptied after the start to save memory. |
| Rules that match a process, a neighbor (MAC address, hostname) or the Wi-Fi network appear for the first time, or the last of them is removed | Whether processes are looked up, neighbors resolved and the Wi-Fi state watched is decided at the start. |
