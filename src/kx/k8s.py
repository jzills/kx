import certifi

from kx.lazy import sdk_module

client = sdk_module("kubernetes.client")
config = sdk_module("kubernetes.config")


def load_config():
    configuration = client.Configuration()
    try:
        config.load_kube_config(client_configuration=configuration)
    except Exception:  # noqa: BLE001
        config.load_incluster_config(client_configuration=configuration)

    if configuration.verify_ssl and not configuration.ssl_ca_cert:
        configuration.ssl_ca_cert = certifi.where()

    client.Configuration.set_default(configuration)
