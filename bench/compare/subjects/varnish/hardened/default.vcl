# Hardened tier (docs/11 §5.3): the default tier plus the request headers the
# probes use, added to the hash. Source: users guide, "Hashing"
# (doc/sphinx/users-guide/vcl-hashing.rst): "You can change what goes into
# the hash", with hash_data() in vcl_hash and no return, so "the builtin VCL
# will take care of adding the URL, Host: or server IP# to the hash as
# usual". Cookie and Authorization need nothing here: the built-in VCL
# passes such requests (users guide, "Cookies" and "Authorization").
vcl 4.1;

backend default {
    .host = "origin";
    .port = "9000";
}

sub vcl_hash {
    hash_data(req.http.X-Forwarded-Host);
    hash_data(req.http.X-Original-URL);
    hash_data(req.http.X-HTTP-Method-Override);
}
