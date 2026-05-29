import { describe, expect, it } from "vitest";
import {
  accountConfigDetail,
  accountConfigEvidenceText,
  accountConfigReview,
  accountConfigSummary,
  accountEnvMatchesFilter,
  accountEnvMatchesQuery,
  accountEnvReviewFindings,
  accountEnvReviewNames,
  accountGitAccessVerifyRequest,
  accountGitRemoteVerifyRequest,
  sshAccessDescription,
  sshPathDisplay,
  sshPathState,
  sshStorageDescription,
} from "./accountConfig";

describe("accountConfig view helpers", () => {
  it("summarizes env and SSH state without exposing secret values", () => {
    const settings = {
      env: [
        { name: "GITHUB_TOKEN", configured: true, updated_at: "2026-05-27T10:00:00Z" },
        { name: "EMPTY_TOKEN", configured: false },
      ],
      ssh: {
        exists: true,
        public_key: "ssh-ed25519 AAAA affent",
        public_key_path: "/state/.affentserve/ssh/id_ed25519.pub",
      },
    };

    expect(accountConfigSummary(settings)).toBe("2 envs · SSH key");
    expect(accountConfigDetail(settings)).toBe("SSH ready · 2 envs");
    expect(sshAccessDescription(settings.ssh)).toContain("Existing keys are never overwritten");
    expect(accountConfigEvidenceText(settings)).toBe([
      "Runtime config evidence",
      "Environment variables: 2",
      "- GITHUB_TOKEN: configured · updated 2026-05-27T10:00:00Z",
      "- EMPTY_TOKEN: empty",
      "SSH: public key ready",
      "SSH public key path: /state/.affentserve/ssh/id_ed25519.pub",
    ].join("\n"));
    expect(accountConfigEvidenceText(settings)).not.toContain("ssh-ed25519 AAAA affent");
    expect(accountConfigReview(settings)).toMatchObject({
      tone: "ready",
      headline: "Private Git ready",
      keyPath: "/state/.affentserve/ssh/id_ed25519.pub",
      keyPathDetail: "custom path",
      envCount: "2 envs",
    });
    expect(sshStorageDescription(settings.ssh)).toBe("/state/.affentserve/ssh/id_ed25519.pub");
    expect(sshPathDisplay("/workspace/.home/.ssh/id_ed25519.pub")).toBe("~/.ssh/id_ed25519.pub");
    expect(sshPathState("/workspace/.home/.ssh/id_ed25519.pub", true)).toBe("standard ~/.ssh");
    expect(accountEnvMatchesQuery(settings.env[0], "github")).toBe(true);
    expect(accountEnvMatchesQuery(settings.env[0], "configured")).toBe(true);
    expect(accountEnvMatchesQuery(settings.env[1], "empty")).toBe(true);
    expect(accountEnvMatchesQuery(settings.env[0], "secret-value")).toBe(false);
  });

  it("builds a bounded SSH verification command for private Git hosts", () => {
    const request = accountGitAccessVerifyRequest("git@GitLab.com:team/private-repo.git");

    expect(request).toEqual({ kind: "host", target: "gitlab.com" });
  });

  it("builds a direct remote verification request for a private repository", () => {
    const request = accountGitRemoteVerifyRequest("git@github.com:team/private-repo.git");

    expect(request).toEqual({ kind: "remote", target: "git@github.com:team/private-repo.git" });
  });

  it("normalizes common HTTPS repository URLs before checking SSH permission", () => {
    expect(accountGitRemoteVerifyRequest("https://github.com/team/private-repo")).toEqual({
      kind: "remote",
      target: "git@github.com:team/private-repo.git",
    });
    expect(accountGitRemoteVerifyRequest("https://gitlab.com/group/subgroup/repo.git")).toEqual({
      kind: "remote",
      target: "git@gitlab.com:group/subgroup/repo.git",
    });
  });

  it("surfaces SSH key issues as runtime config evidence", () => {
    const settings = {
      env: [],
      ssh: {
        exists: true,
        public_key_error: "could not derive public key",
      },
    };

    expect(accountConfigSummary(settings)).toBe("SSH key issue");
    expect(accountConfigDetail(settings)).toBe("SSH key found; public key unavailable");
    expect(accountConfigReview(settings)).toMatchObject({
      tone: "attention",
      privateGit: "Blocked",
      publicKey: "Unavailable",
      nextAction: "Fix or derive the public key in ~/.ssh, then refresh config before cloning private repositories.",
    });
    expect(sshStorageDescription(settings.ssh)).toBe("Storage path not reported by this server build.");
    expect(accountConfigEvidenceText(settings)).toContain("SSH issue: could not derive public key");
  });

  it("finds environment variables that need runtime config review", () => {
    const settings = {
      env: [
        { name: "GOOGLE_API_KEY", configured: true },
        { name: "EMPTY_TOKEN", configured: false },
        { name: "GITHUB_TOKEN", configured: true },
      ],
      ssh: { exists: false },
    };

    expect(accountEnvReviewFindings(settings)).toEqual([
      { kind: "empty", name: "EMPTY_TOKEN", detail: "saved with an empty value" },
      {
        kind: "incomplete",
        name: "GOOGLE_API_KEY",
        detail: "Google search also needs GOOGLE_CSE_ID or GOOGLE_SEARCH_ENGINE_ID",
        related: ["GOOGLE_CSE_ID", "GOOGLE_SEARCH_ENGINE_ID"],
      },
    ]);
    expect(accountEnvReviewNames(settings)).toEqual(new Set(["EMPTY_TOKEN", "GOOGLE_API_KEY", "GOOGLE_CSE_ID", "GOOGLE_SEARCH_ENGINE_ID"]));
    expect(accountEnvMatchesFilter(settings.env[0], "configured", accountEnvReviewNames(settings))).toBe(true);
    expect(accountEnvMatchesFilter(settings.env[1], "empty", accountEnvReviewNames(settings))).toBe(true);
    expect(accountEnvMatchesFilter(settings.env[2], "review", accountEnvReviewNames(settings))).toBe(false);
  });
});
