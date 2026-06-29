{
  description = "md2 - convert markdown files to PDF, HTML, and text";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        version = if (self ? rev) then self.rev else "dev";
        md2 = pkgs.buildGoModule {
          pname = "md2";
          inherit version;
          src = ./.;
          vendorHash = "sha256-6ZFEs7zWAVFTJ08UPEt3cAw8VRiEOjbtEEByI7E4UaU=";
          ldflags = [ "-s" "-w" "-X" "main.version=${version}" ];
          # PDF browser fallback downloads Chromium at runtime; no build-time dep.
          doCheck = true;
          meta = with pkgs.lib; {
            description = "Convert markdown files to PDF, HTML, and text";
            homepage = "https://github.com/rapatao/md2";
            license = licenses.mit;
            mainProgram = "md2";
          };
        };
      in
      {
        packages.default = md2;
        packages.md2 = md2;
        apps.default = flake-utils.lib.mkApp { drv = md2; };
      });
}
