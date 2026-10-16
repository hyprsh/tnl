# Routes from an Outpost

On an Outpost's host, `tnl route add`, `edit`, `rm` and `ls`, and `tnl zone ls`, work through the Outpost's `tnl.sock`. The Outpost forwards them to the Edge, which takes the Outpost as the caller.

```sh
tnl route add app.example.com http://localhost:3000
tnl route ls
```

- There is no `--outpost`: every Route you add goes through this Outpost, and `--outpost` is refused.
- You can change or remove only Routes through this Outpost, and you can't move one onto another Outpost. A Hostname another Outpost holds is refused, naming the holder.
- If the Edge can't be reached, the command exits 1 with "Outpost not connected".
- The `tnl` you run must be the exact version of the running Outpost. If not, it refuses before sending anything and tells you to restart the Outpost's unit.
- The Outpost logs each Route change made through its CLI at `info`, and the Edge logs it with `actor=outpost:<name>`.
