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
            pname = "codex-profile";
            version = "0.1.0";
            src = ./.;
            vendorHash = "sha256-6vaEOGaj2CssAdEvabxP+2s3M86ZJ9XSlAL++w1Iqx8=";

            nativeBuildInputs = [ pkgs.makeWrapper ];

            postInstall = ''
              mv $out/bin/codex-profiled $out/bin/codex-profile

              mkdir -p \
                $out/share/bash-completion/completions \
                $out/share/zsh/site-functions \
                $out/share/fish/vendor_completions.d

              $out/bin/codex-profile completion bash > \
                $out/share/bash-completion/completions/codex-profile
              printf '%s\n' \
                "source \"$out/share/bash-completion/completions/codex-profile\"" \
                'if [[ $(type -t compopt) = "builtin" ]]; then' \
                '  complete -o default -F __start_codex-profile c' \
                'else' \
                '  complete -o default -o nospace -F __start_codex-profile c' \
                'fi' \
                > $out/share/bash-completion/completions/c

              $out/bin/codex-profile completion zsh > \
                $out/share/zsh/site-functions/_codex-profile
              sed -i '1s/#compdef codex-profile/#compdef codex-profile c/' \
                $out/share/zsh/site-functions/_codex-profile
              sed -i 's/compdef _codex-profile codex-profile/compdef _codex-profile codex-profile c/' \
                $out/share/zsh/site-functions/_codex-profile

              $out/bin/codex-profile completion fish > \
                $out/share/fish/vendor_completions.d/codex-profile.fish

              wrapProgram $out/bin/codex-profile \
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
            program = "${package}/bin/codex-profile";
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
