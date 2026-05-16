{
  description = "run the same Codex configuration under different accounts";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      supportedSystems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    in
    {
      packages = forAllSystems (system:
        let
          pkgs = import nixpkgs { inherit system; };
        in {
          default = pkgs.buildGoModule {
            pname = "codex-profiled";
            version = "0.1.0";
            src = ./.;
            vendorHash = "sha256-6vaEOGaj2CssAdEvabxP+2s3M86ZJ9XSlAL++w1Iqx8=";

            nativeBuildInputs = [ pkgs.makeWrapper ];

            postInstall = ''
              mkdir -p \
                $out/share/bash-completion/completions \
                $out/share/zsh/site-functions \
                $out/share/fish/vendor_completions.d

              $out/bin/codex-profiled completion bash > \
                $out/share/bash-completion/completions/codex-profiled
              printf '%s\n' \
                "source \"$out/share/bash-completion/completions/codex-profiled\"" \
                'if [[ $(type -t compopt) = "builtin" ]]; then' \
                '  complete -o default -F __start_codex-profiled c' \
                'else' \
                '  complete -o default -o nospace -F __start_codex-profiled c' \
                'fi' \
                > $out/share/bash-completion/completions/c

              $out/bin/codex-profiled completion zsh > \
                $out/share/zsh/site-functions/_codex-profiled
              sed -i '1s/#compdef codex-profiled/#compdef codex-profiled c/' \
                $out/share/zsh/site-functions/_codex-profiled
              sed -i 's/compdef _codex-profiled codex-profiled/compdef _codex-profiled codex-profiled c/' \
                $out/share/zsh/site-functions/_codex-profiled

              $out/bin/codex-profiled completion fish > \
                $out/share/fish/vendor_completions.d/codex-profiled.fish

              wrapProgram $out/bin/codex-profiled \
                --prefix PATH : /run/wrappers/bin:${pkgs.lib.makeBinPath [ pkgs.fuse3 ]}
            '';
          };
        });

      apps = forAllSystems (system:
        let
          package = self.packages.${system}.default;
        in {
          default = {
            type = "app";
            program = "${package}/bin/codex-profiled";
          };
        });

      devShells = forAllSystems (system:
        let
          pkgs = import nixpkgs { inherit system; };
        in {
          default = pkgs.mkShell {
            packages = [
              pkgs.fuse3
              pkgs.go
              pkgs.gopls
            ];
          };
        });
    };
}
