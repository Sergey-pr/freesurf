package main

var assetDigests = map[string]string{
	// sing-box 1.13.21
	"sing-box-1.13.21-darwin-amd64.tar.gz":              "61093d79211a6ae7b707d30f07be35b1167ca8366bf0dbc06ee5fb35c90dc9e8",
	"sing-box-1.13.21-darwin-arm64.tar.gz":              "62bca85bf08b9145288729cf010c98ea9877b8086f7369cde9e127012d509424",
	"sing-box-1.13.21-windows-amd64.zip":                "a03291793d3a3c6e266447a58140657ac099ff278abf3b8ff678932356a62ced",
	"sing-box-1.13.21-windows-arm64.zip":                "ef752d9bffd6d590dd6886b28819a7ae4efee70b8188f9198757d91570efd554",
	"sing-box-1.13.21-windows-386-legacy-windows-7.zip": "0b8e37d9c441a5f18b37d652bd93ff235c77c7944385e87fdded095a9876e534",

	// Xray-core 26.3.27
	"Xray-macos-64.zip":          "f5b0471d3459eff1b82e48af0aeac186abcc3298210070afbbbd8437a4e8b203",
	"Xray-macos-arm64-v8a.zip":   "2e93a67e8aa1936ecefb307e120830fcbd4c643ab9b1c46a2d0838d5f8409eaf",
	"Xray-windows-64.zip":        "d004c39288ce9ada487c6f398c7c545f7d749e44bdfdd59dbc9f865afba4e1ad",
	"Xray-windows-arm64-v8a.zip": "35d4ed6ec21224fb22b07c2c3f672e2350cd536f2c74d309150175a76365ea88",
	"Xray-windows-32.zip":        "956a5ec00bce747c7936dc4ff7ac570df1c8030b0a4a8640f843488365084db3",

	// Rule-sets at the commits pinned in ruleSets
	"geoip-ru.srs":            "6e23f5580dd443e2f9c4895adafee8d199c1a3487182ad5f0a2256cf59c7e53a",
	"geosite-category-ru.srs": "c36e157adf86edf7b722b51f3acb93bbb2a7f8083932dae29b4b5ef2c1ced870",
}
