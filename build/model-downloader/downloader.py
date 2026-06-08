import argparse
import json
import os
import sys
import time
from pathlib import Path


def emit(event_type: str, **kwargs):
    payload = {
        "type": event_type,
        "time": int(time.time()),
        **kwargs,
    }
    print(json.dumps(payload, ensure_ascii=False), flush=True)


def ensure_dir(path: str):
    Path(path).mkdir(parents=True, exist_ok=True)


def download_from_huggingface(source_uri: str, revision: str, local_path: str):
    from huggingface_hub import snapshot_download

    emit("progress", progress=10, message="checking huggingface model repository")

    kwargs = {
        "repo_id": source_uri,
        "local_dir": local_path,
        "local_dir_use_symlinks": False,
    }

    if revision:
        kwargs["revision"] = revision

    token = os.environ.get("HF_TOKEN") or os.environ.get("HUGGINGFACE_TOKEN")
    if token:
        kwargs["token"] = token

    emit("progress", progress=20, message="starting huggingface snapshot download")

    result_path = snapshot_download(**kwargs)

    emit(
        "progress",
        progress=95,
        message="huggingface snapshot download completed",
        downloaded_path=result_path,
    )

    return result_path


def download_from_modelscope(source_uri: str, revision: str, local_path: str):
    from modelscope import snapshot_download

    emit("progress", progress=10, message="checking modelscope model repository")
    emit("progress", progress=20, message="starting modelscope snapshot download")

    kwargs = {
        "model_id": source_uri,
        "local_dir": local_path,
    }

    if revision:
        kwargs["revision"] = revision

    result_path = snapshot_download(**kwargs)

    emit(
        "progress",
        progress=95,
        message="modelscope snapshot download completed",
        downloaded_path=result_path,
    )

    return result_path


def main():
    parser = argparse.ArgumentParser(description="LLM platform model downloader")

    parser.add_argument("--source-type", required=True, choices=["huggingface", "modelscope"])
    parser.add_argument("--source-uri", required=True)
    parser.add_argument("--revision", default="main")
    parser.add_argument("--local-path", required=True)

    args = parser.parse_args()

    local_path = os.path.abspath(args.local_path)
    ensure_dir(local_path)

    emit(
        "started",
        progress=1,
        message="model downloader started",
        source_type=args.source_type,
        source_uri=args.source_uri,
        revision=args.revision,
        local_path=local_path,
    )

    try:
        if args.source_type == "huggingface":
            result_path = download_from_huggingface(
                source_uri=args.source_uri,
                revision=args.revision,
                local_path=local_path,
            )
        elif args.source_type == "modelscope":
            result_path = download_from_modelscope(
                source_uri=args.source_uri,
                revision=args.revision,
                local_path=local_path,
            )
        else:
            raise ValueError(f"unsupported source type: {args.source_type}")

        emit(
            "completed",
            progress=100,
            message="model download completed",
            local_path=local_path,
            result_path=result_path,
        )

        return 0

    except Exception as exc:
        emit(
            "failed",
            progress=0,
            message="model download failed",
            error=str(exc),
        )
        return 1


if __name__ == "__main__":
    sys.exit(main())