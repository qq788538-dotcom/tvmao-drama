#!/usr/bin/env python3
"""Validate the sentence-to-script coverage file of the novel short-drama skill."""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Any, Dict, List, Mapping, Optional, Tuple


TOP_LEVEL_FIELDS = {"sourceText", "sourceSentences", "scriptLines", "scriptText"}
COMPLETE_UTTERANCE_END_RE = re.compile(r"(?:[。！？!?]|…+|——)[”\"’'）)]*$")
QUESTION_PARTICLE_GAP_RE = re.compile(r"[吗么][ \t]+[\u3400-\u9fff]")
SOURCE_SENTENCE_ENDS = set("。！？!?")
SOURCE_SENTENCE_CLOSERS = set("”\"’'）》】）)")


def load_json(path: Path) -> Tuple[Optional[Any], List[str]]:
    try:
        return json.loads(path.read_text(encoding="utf-8")), []
    except OSError as exc:
        return None, [f"无法读取 JSON 文件 {path}: {exc}"]
    except json.JSONDecodeError as exc:
        return None, [f"JSON 解析失败（第 {exc.lineno} 行第 {exc.colno} 列）: {exc.msg}"]


def _index_records(
    data: Mapping[str, Any],
    field: str,
    label: str,
    id_prefix: str,
    ref_field: str,
    errors: List[str],
) -> Tuple[Dict[str, List[str]], Dict[str, List[str]]]:
    records = data.get(field)
    if not isinstance(records, list) or not records:
        errors.append(f"{field} 必须是非空数组")
        return {}, {}

    indexed: Dict[str, List[str]] = {}
    refs: Dict[str, List[str]] = {}
    for index, record in enumerate(records):
        location = f"{field}[{index}]"
        if not isinstance(record, dict):
            errors.append(f"{location} 必须是对象")
            continue
        identifier = record.get("id")
        text = record.get("text")
        linked_ids = record.get(ref_field)
        if not isinstance(identifier, str) or not identifier.startswith(id_prefix) or not identifier[len(id_prefix):].isdigit():
            errors.append(f"{location}.id 必须是 {id_prefix} 加数字编号")
            continue
        if identifier in indexed:
            errors.append(f"{label}编号重复: {identifier}")
            continue
        indexed[identifier] = []
        if not isinstance(text, str) or not text.strip():
            errors.append(f"{location}.text 必须是非空字符串")
        if not isinstance(linked_ids, list) or any(not isinstance(value, str) or not value for value in linked_ids):
            errors.append(f"{location}.{ref_field} 必须是字符串编号数组")
            refs[identifier] = []
            continue
        if len(set(linked_ids)) != len(linked_ids):
            errors.append(f"{location}.{ref_field} 不得包含重复编号")
        refs[identifier] = linked_ids
    return indexed, refs


def split_source_sentences(source_text: str) -> List[str]:
    """Split Chinese prose into ordered non-empty sentence units without dropping text."""
    sentences: List[str] = []
    buffer: List[str] = []
    normalized = source_text.replace("\r\n", "\n").replace("\r", "\n")
    index = 0
    while index < len(normalized):
        char = normalized[index]
        buffer.append(char)
        if char in SOURCE_SENTENCE_ENDS:
            while (
                index + 1 < len(normalized)
                and normalized[index + 1] in SOURCE_SENTENCE_ENDS | SOURCE_SENTENCE_CLOSERS
            ):
                index += 1
                buffer.append(normalized[index])
            sentence = "".join(buffer).strip()
            if sentence:
                sentences.append(sentence)
            buffer = []
        elif char == "\n":
            sentence = "".join(buffer).strip()
            if sentence:
                sentences.append(sentence)
            buffer = []
        index += 1
    sentence = "".join(buffer).strip()
    if sentence:
        sentences.append(sentence)
    return sentences


def _spoken_body(text: str) -> str:
    parts = re.split(r"[：:]", text, maxsplit=1)
    return parts[1].strip() if len(parts) == 2 else text.strip()


def _semantic_chars(text: str) -> str:
    """Normalize away punctuation/spacing while preserving every spoken word and digit."""
    return "".join(char for char in text if char.isalnum())


def _validate_semantic_coverage(data: Mapping[str, Any], errors: List[str]) -> None:
    source_records = data.get("sourceSentences")
    line_records = data.get("scriptLines")
    script_text = data.get("scriptText")
    if not isinstance(source_records, list) or not isinstance(line_records, list):
        return

    source_by_id = {
        record.get("id"): record
        for record in source_records
        if isinstance(record, dict) and isinstance(record.get("id"), str)
    }
    line_by_id = {
        record.get("id"): record
        for record in line_records
        if isinstance(record, dict) and isinstance(record.get("id"), str)
    }

    expected_line_ids = [f"L{index:03d}" for index in range(1, len(line_records) + 1)]
    actual_line_ids = [
        record.get("id") for record in line_records if isinstance(record, dict)
    ]
    if actual_line_ids != expected_line_ids:
        errors.append("scriptLines 编号必须从 L001 按剧本顺序连续递增")

    actual_source_order: List[str] = []
    for record in line_records:
        if not isinstance(record, dict):
            continue
        source_ids = record.get("sourceSentenceIds")
        if isinstance(source_ids, list) and len(source_ids) == 1:
            actual_source_order.append(source_ids[0])
    source_positions = {source_id: index for index, source_id in enumerate(source_by_id)}
    line_positions = {line_id: index for index, line_id in enumerate(line_by_id)}
    known_positions = [
        source_positions[source_id]
        for source_id in actual_source_order
        if source_id in source_positions
    ]
    if known_positions != sorted(known_positions):
        errors.append("scriptLines 必须保持 sourceSentences 的原文顺序")

    for source_id, source_record in source_by_id.items():
        source_text = source_record.get("text")
        line_ids = source_record.get("scriptLineIds")
        if not isinstance(source_text, str) or not isinstance(line_ids, list) or not line_ids:
            continue
        known_line_positions = [
            line_positions[line_id] for line_id in line_ids if line_id in line_positions
        ]
        if known_line_positions != sorted(known_line_positions):
            errors.append(f"{source_id}.scriptLineIds 必须保持 scriptLines 顺序")
        bodies: List[str] = []
        complete = True
        for line_id in line_ids:
            line_record = line_by_id.get(line_id)
            if not isinstance(line_record, dict) or not isinstance(line_record.get("text"), str):
                complete = False
                break
            bodies.append(_spoken_body(line_record["text"]))
        if complete and _semantic_chars("".join(bodies)) != _semantic_chars(source_text):
            errors.append(f"{source_id} 映射的剧本行正文与原文句字词不一致")

    if isinstance(script_text, str):
        rendered_lines = [line.strip() for line in script_text.splitlines() if line.strip()]
        cursor = 0
        for record in line_records:
            if not isinstance(record, dict) or not isinstance(record.get("text"), str):
                continue
            expected = record["text"].strip()
            while cursor < len(rendered_lines) and rendered_lines[cursor] != expected:
                cursor += 1
            if cursor >= len(rendered_lines):
                errors.append("scriptText 未按顺序完整包含 scriptLines")
                break
            cursor += 1


def _validate_script_line_text(records: Any, errors: List[str]) -> None:
    if not isinstance(records, list):
        return
    for index, record in enumerate(records):
        if not isinstance(record, dict):
            continue
        text = record.get("text")
        if not isinstance(text, str):
            continue
        location = f"scriptLines[{index}].text"
        if text.count("“") != text.count("”") or text.count('"') % 2 != 0:
            errors.append(f"{location} 引号必须成对")
        parts = re.split(r"[：:]", text, maxsplit=1)
        if len(parts) != 2:
            errors.append(f"{location} 发声行必须包含说话人与冒号后的正文")
            continue
        body = parts[1].strip()
        if body and not re.sub(r"[\s“”\"'‘’、，。！？!?；;：:]", "", body):
            errors.append(f"{location} 正文不能只含标点或引号")
            continue
        if body and not COMPLETE_UTTERANCE_END_RE.search(body):
            errors.append(f"{location} 发声行必须以完整句末标点结束")
        if QUESTION_PARTICLE_GAP_RE.search(body):
            errors.append(f"{location} 疑问语气词后缺少问号，不能用空白连接下一分句")


def _strip_whitespace(text: str) -> str:
    return "".join(text.split())


def _validate_source_anchor(data: Mapping[str, Any], source_anchor: str, errors: List[str]) -> None:
    """Compare sourceText against the machine-written anchor so dropped prose fails loudly.

    sourceText 由模型抄写，会被非确定性地摘要/截断；锚点文件由后端直接落盘，
    是唯一可信的原文。只忽略空白差异（换行、缩进不承载语音内容），任何字词或
    标点缺失都必须报错。
    """
    source_text = data.get("sourceText")
    if not isinstance(source_text, str):
        return
    if _strip_whitespace(source_text) != _strip_whitespace(source_anchor):
        errors.append(
            "sourceText 与原文锚点 source 不一致"
            f"（锚点 {len(_strip_whitespace(source_anchor))} 字，sourceText {len(_strip_whitespace(source_text))} 字）；"
            "sourceText 必须从 source.txt 原样注入，不得手抄或摘要"
        )


def validate_adapted_script(data: Any, source_anchor: Optional[str] = None) -> List[str]:
    """Return every schema and sentence-coverage error in adapted_script.json."""
    errors: List[str] = []
    if not isinstance(data, dict):
        return ["JSON 顶层必须是对象"]
    if source_anchor is not None:
        _validate_source_anchor(data, source_anchor, errors)
    if set(data) != TOP_LEVEL_FIELDS:
        errors.append("顶层字段必须且只能为 sourceText、sourceSentences、scriptLines、scriptText")
    source_text = data.get("sourceText")
    if not isinstance(source_text, str) or not source_text.strip():
        errors.append("sourceText 必须是非空原文字符串")
    if not isinstance(data.get("scriptText"), str) or not data["scriptText"].strip():
        errors.append("scriptText 必须是非空字符串")

    sources, source_to_lines = _index_records(
        data, "sourceSentences", "原文句", "S", "scriptLineIds", errors
    )
    lines, line_to_sources = _index_records(
        data, "scriptLines", "剧本行", "L", "sourceSentenceIds", errors
    )
    _validate_script_line_text(data.get("scriptLines"), errors)

    source_records = data.get("sourceSentences")
    if isinstance(source_text, str) and isinstance(source_records, list):
        expected_sentences = split_source_sentences(source_text)
        actual_sentences = [record.get("text") for record in source_records if isinstance(record, dict)]
        if actual_sentences != expected_sentences:
            errors.append("sourceSentences 未逐句覆盖 sourceText")
        expected_ids = [f"S{index:03d}" for index in range(1, len(expected_sentences) + 1)]
        if list(sources) != expected_ids:
            errors.append("sourceSentences 编号必须从 S001 按原文顺序连续递增")

    for source_id, line_ids in source_to_lines.items():
        if not line_ids:
            errors.append(f"{source_id} 未映射到任何剧本行")
        for line_id in line_ids:
            if line_id not in lines:
                errors.append(f"{source_id} 指向的剧本行 {line_id} 不存在")
            elif source_id not in line_to_sources.get(line_id, []):
                errors.append(f"{source_id} 与 {line_id} 的映射不一致")

    for line_id, source_ids in line_to_sources.items():
        if len(source_ids) != 1:
            errors.append(f"{line_id} 必须且只能映射一个原文句子")
        for source_id in source_ids:
            if source_id not in sources:
                errors.append(f"{line_id} 指向的原文句子 {source_id} 不存在")
            elif line_id not in source_to_lines.get(source_id, []):
                errors.append(f"{source_id} 与 {line_id} 的映射不一致")
    _validate_semantic_coverage(data, errors)
    return errors


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("json_file", type=Path, help="待校验的 adapted_script.json")
    parser.add_argument(
        "--source",
        type=Path,
        default=None,
        help="后端机器写入的原文锚点文件（source.txt），sourceText 必须与其一致",
    )
    args = parser.parse_args(argv)
    data, errors = load_json(args.json_file)
    source_anchor: Optional[str] = None
    if args.source is not None:
        try:
            source_anchor = args.source.read_text(encoding="utf-8")
        except OSError as exc:
            errors.append(f"无法读取原文锚点文件 {args.source}: {exc}")
    if data is not None:
        errors.extend(validate_adapted_script(data, source_anchor=source_anchor))
    if errors:
        for error in errors:
            print(f"ERROR: {error}", file=sys.stderr)
        return 1
    print("OK: adapted script JSON is valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
