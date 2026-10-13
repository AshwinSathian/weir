# Default tier: a backend and the built-in VCL (default_grace is 10 s).
vcl 4.1;

backend default {
    .host = "origin";
    .port = "9000";
}
