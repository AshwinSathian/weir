# Tuned tier. Grace and keep from the users guide (vcl-grace). The backend
# cap and its queue (vcl-backend: .max_connections, .wait_limit,
# .wait_timeout, since 7.6) are set to Weir's default limiter numbers
# (64 concurrent, 8192 queued, 5 s wait) so the two are compared like for like.
vcl 4.1;

backend default {
    .host = "origin";
    .port = "9000";
    .max_connections = 64;
    .wait_limit = 8192;
    .wait_timeout = 5s;
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
