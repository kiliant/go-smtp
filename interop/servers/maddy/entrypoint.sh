#!/bin/sh
set -eu

# /data is a volume in the upstream image, so certificates generated while
# building the image are discarded when the container starts. Create the
# disposable test certificate after that volume is mounted instead.
mkdir -p /data/certs
if [ ! -s /data/certs/cert.pem ] || [ ! -s /data/certs/key.pem ]; then
	openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
		-keyout /data/certs/key.pem -out /data/certs/cert.pem \
		-subj /CN=mail.example.test
fi

# The imapsql backend intentionally has no implicit users. Provision the
# fixture mailbox before starting the SMTP listener; repeated creation is
# harmless when a persisted /data volume is reused locally.
/bin/maddy -config /data/maddy.conf imap-acct create interop@example.test >/dev/null 2>&1 || true

exec /bin/maddy -config /data/maddy.conf run
