import json

with open("coverage.json") as f:
    data = json.load(f)
    files = data["files"]
    print("已有部分覆盖率的模块:")
    for file, info in sorted(
        files.items(), key=lambda x: x[1]["summary"]["percent_covered"], reverse=True
    ):
        if "core" in file and 0 < info["summary"]["percent_covered"] < 90:
            coverage = info["summary"]["percent_covered"]
            statements = info["summary"]["num_statements"]
            covered = info["summary"]["covered_lines"]
            missing = statements - covered
            print(f"{file}: {coverage:.1f}% ({statements}行, 已覆盖{covered}行, 缺{missing}行)")
