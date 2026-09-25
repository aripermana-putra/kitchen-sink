import sys
from cryptography import x509
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.hazmat.primitives.serialization import Encoding


def rebuild(cert, issuer_name, signing_key, keep_public_key=True):
    builder = x509.CertificateBuilder()
    builder = builder.subject_name(cert.subject)
    builder = builder.issuer_name(issuer_name)
    builder = builder.public_key(cert.public_key() if keep_public_key else signing_key.public_key())
    builder = builder.serial_number(cert.serial_number)
    builder = builder.not_valid_before(cert.not_valid_before_utc)
    builder = builder.not_valid_after(cert.not_valid_after_utc)
    has_key_usage = False
    for ext in cert.extensions:
        if isinstance(ext.value, x509.BasicConstraints):
            builder = builder.add_extension(ext.value, critical=True)
        elif isinstance(ext.value, x509.KeyUsage):
            has_key_usage = True
            builder = builder.add_extension(ext.value, critical=True)
        else:
            builder = builder.add_extension(ext.value, critical=ext.critical)
    if not has_key_usage:
        builder = builder.add_extension(
            x509.KeyUsage(
                digital_signature=False,
                content_commitment=False,
                key_encipherment=False,
                data_encipherment=False,
                key_agreement=False,
                key_cert_sign=True,
                crl_sign=True,
                encipher_only=False,
                decipher_only=False,
            ),
            critical=True,
        )
    return builder.sign(signing_key, hashes.SHA256())


def load(path):
    with open(path, "rb") as f:
        return x509.load_pem_x509_certificate(f.read())


def write(cert, path):
    with open(path, "wb") as f:
        f.write(cert.public_bytes(Encoding.PEM))


if __name__ == "__main__":
    # sys.argv: root_in root_out intermediate_in intermediate_out
    root_in, root_out, inter_in, inter_out = sys.argv[1:5]

    root = load(root_in)
    inter = load(inter_in)

    # One throwaway key stands in for the root's real private key. The root
    # is re-self-signed with it; the intermediate is re-signed BY it (so the
    # chain still links), but the intermediate KEEPS its original public key
    # -- the real intercepted leaf cert's signature was made against that
    # original key, and must still validate against it.
    throwaway_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)

    new_root = rebuild(root, root.subject, throwaway_key, keep_public_key=False)
    new_inter = rebuild(inter, root.subject, throwaway_key, keep_public_key=True)

    write(new_root, root_out)
    write(new_inter, inter_out)
    print(f"{root_in} -> {root_out} (critical=True, re-self-signed)")
    print(f"{inter_in} -> {inter_out} (critical=True, re-signed by patched root, original public key kept)")
