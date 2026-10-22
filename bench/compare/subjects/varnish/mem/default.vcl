# Default tier: a backend and the built-in VCL (default_grace is 10 s).
# The PURGE rule is the one from the users guide ("Purging and banning",
# HTTP Purging), there so purge-herd can run; the ACL is the private address
# ranges a Compose network can get.
vcl 4.1;

backend default {
    .host = "origin";
    .port = "9000";
}

acl purge {
    "localhost";
    "10.0.0.0"/8;
    "172.16.0.0"/12;
    "192.168.0.0"/16;
}

sub vcl_recv {
    if (req.method == "PURGE") {
        if (!client.ip ~ purge) {
            return (synth(405, "Not allowed."));
        }
        return (purge);
    }
}
