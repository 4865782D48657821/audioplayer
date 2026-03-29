{
	description = "Hugo Dev Environment";

	inputs ={
		nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
		utils.url = "github:numtide/flake-utils";
	};

	outputs = {self, nixpkgs, utils }:
	utils.lib.eachDefaultSystem (system:
		let
			pkgs = import nixpkgs {inherit system; };
		in
		{
			devShells.default = pkgs.mkShell {
				buildInputs = with pkgs;  [
					go
					pkg-config
					alsa-lib
					gotools
					gopls
					delve
					golangci-lint
					gomodifytags
					impl
					git
					nodePackages.prettier
				];
			shellHook = ''
			echo "Go Development Env ready"
			go version
			'';
			};
		}
		);
}
