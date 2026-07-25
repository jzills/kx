from unittest.mock import patch

from kx.k8s import load_config


@patch("kx.k8s.client.Configuration.set_default")
@patch("kx.k8s.certifi.where", return_value="/bundled/cacert.pem")
@patch("kx.k8s.config.load_kube_config")
def test_load_config_uses_bundled_ca_when_kubeconfig_has_none(
    load_kube_config, certifi_where, set_default
):
    load_kube_config.side_effect = lambda client_configuration: None

    load_config()

    configuration = set_default.call_args.args[0]
    assert configuration.ssl_ca_cert == "/bundled/cacert.pem"
    certifi_where.assert_called_once_with()


@patch("kx.k8s.client.Configuration.set_default")
@patch("kx.k8s.certifi.where")
@patch("kx.k8s.config.load_kube_config")
def test_load_config_preserves_kubeconfig_ca(
    load_kube_config, certifi_where, set_default
):
    def configure(*, client_configuration):
        client_configuration.ssl_ca_cert = "/cluster/ca.pem"

    load_kube_config.side_effect = configure

    load_config()

    configuration = set_default.call_args.args[0]
    assert configuration.ssl_ca_cert == "/cluster/ca.pem"
    certifi_where.assert_not_called()


@patch("kx.k8s.client.Configuration.set_default")
@patch("kx.k8s.certifi.where")
@patch("kx.k8s.config.load_kube_config")
def test_load_config_does_not_add_ca_when_verification_is_disabled(
    load_kube_config, certifi_where, set_default
):
    def configure(*, client_configuration):
        client_configuration.verify_ssl = False

    load_kube_config.side_effect = configure

    load_config()

    configuration = set_default.call_args.args[0]
    assert configuration.ssl_ca_cert is None
    certifi_where.assert_not_called()


@patch("kx.k8s.client.Configuration.set_default")
@patch("kx.k8s.certifi.where")
@patch("kx.k8s.config.load_incluster_config")
@patch("kx.k8s.config.load_kube_config", side_effect=OSError)
def test_load_config_falls_back_to_incluster_config(
    load_kube_config, load_incluster_config, certifi_where, set_default
):
    def configure(*, client_configuration):
        client_configuration.ssl_ca_cert = "/service-account/ca.crt"

    load_incluster_config.side_effect = configure

    load_config()

    load_incluster_config.assert_called_once()
    configuration = set_default.call_args.args[0]
    assert configuration.ssl_ca_cert == "/service-account/ca.crt"
    certifi_where.assert_not_called()
