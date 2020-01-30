load("build_defs.bzl", "packetdrill_test")

package(licenses = ["notice"])

packetdrill_test(
    name = "fin_wait2_timeout",
    init_script = "packetdrill_setup.sh",
    scripts = [
        "fin_wait2_timeout.pkt",
    ],
    tags = ["local"],
    test_runner = "packetdrill_test.sh",
)
