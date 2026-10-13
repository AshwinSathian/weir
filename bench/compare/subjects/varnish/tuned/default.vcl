# Tuned tier. Grace and keep from the users guide (vcl-grace). The backend
# cap and its queue (vcl-backend: .max_connections, .wait_limit,
# .wait_timeout, since 7.6) are set to Weir's default limiter numbers
# (64 concurrent, 8192 queued, 5 s wait) so the two are compared like for like.
# Purging is the soft purge of the bundled vmod_purge, following the example
# in its manual (vmod_purge(3), EXAMPLE): the object's lifetime ends and its
# grace still applies, so a purge does not turn into a cold start.
vcl 4.1;

import purge;

backend default {
    .host = "origin";
    .port = "9000";
    .max_connections = 64;
    .wait_limit = 8192;
    .wait_timeout = 5s;
}

acl purge_acl {
    "localhost";
    "10.0.0.0"/8;
    "172.16.0.0"/12;
    "192.168.0.0"/16;
}

sub vcl_recv {
    if (req.method == "PURGE") {
        if (client.ip !~ purge_acl) {
            return (synth(405));
        }
        return (hash);
    }
}

sub soft_purge {
    set req.http.purged = purge.soft();
    return (synth(200));
}

sub vcl_hit {
    if (req.method == "PURGE") {
        call soft_purge;
    }
}

sub vcl_miss {
    if (req.method == "PURGE") {
        call soft_purge;
    }
}

sub vcl_backend_response {
    set beresp.grace = 60s;
    set beresp.keep = 5m;
    # vcl-grace, "Misbehaving servers": a failed background fetch must not
    # replace the object it was refreshing.
    if (beresp.status >= 500 && bereq.is_bgfetch) {
        return (abandon);
    }
}
