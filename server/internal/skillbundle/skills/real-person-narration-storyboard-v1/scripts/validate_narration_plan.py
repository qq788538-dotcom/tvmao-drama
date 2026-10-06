#!/usr/bin/env python3
"""Validate the source-traceable narration plan used by this project skill."""

from __future__ import annotations

import argparse
import json
import math
import re
import sys
from pathlib import Path
from typing import Any, List, Mapping, Optional, Sequence, Tuple


TOP_LEVEL_FIELDS = [
    "inputType",
    "narrationMode",
    "fidelityMode",
    "baseSpeechCharsPerSec",
    "playbackRate",
    "genreProfile",
    "sourceText",
    "sourceUnits",
    "narrationUnits",
]
SOURCE_UNIT_FIELDS = ["id", "text", "narrationUnitIds"]
NARRATION_UNIT_FIELDS = [
    "id",
    "speaker",
    "mode",
    "text",
    "sourceUnitIds",
    "targetSec",
    "pauseAfterSec",
    "hookRole",
    "visualMode",
    "visualIntent",
    "visualEvidence",
    "spoilerBoundary",
    "scene",
]

INPUT_TYPES = {"narration_script", "screenplay", "short_story"}
NARRATION_MODES = {"fast_recap"}
FIDELITY_MODES = {"faithful", "condensed"}
GENRE_PROFILES = {
    "general_drama",
    "modern_romance",
    "costume_intrigue",
    "suspense",
    "comeback",
}
VOICE_MODES = {"narration", "dialogue", "os", "vo", "system"}
VISUAL_MODES = {
    "enactment",
    "evidence",
    "reaction",
    "establishing",
    "transition",
    "flashback",
    "dialogue",
}
SPEECH_END_RE = re.compile(r"(?:……|[。！？!?])$")
SEMANTIC_RE = re.compile(r"[\u3400-\u9fffA-Za-z0-9]")
BASE_SPEECH_CHARS_PER_SEC = 3
DEFAULT_PLAYBACK_RATE = 1.6
ALLOWED_PLAYBACK_RATES = {1.5, 1.6, 1.7, 1.8}
MAX_NARRATION_SEMANTIC_CHARS = 14
MAX_OTHER_VOICE_SEMANTIC_CHARS = 9
PAUSE_AFTER_SECONDS = {0, 0.5}
PAUSE_BOUNDARY_RE = re.compile(r"反转|揭晓|真相|身份|重大结果|结果落定|时空|转场|切换")
HOOK_ROLES = {"primary", "support", "none"}
ANONYMOUS_NARRATORS = {"解说", "旁白"}
SYSTEM_SPEAKERS = {"系统声", "机械的电子音女声", "广播声", "电话声", "录音声"}


def load_json(path: Path) -> Tuple[Optional[Any], List[str]]:
    try:
        text = path.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as exc:
        return None, [f"无法读取 JSON 文件 {path}: {exc}"]
    try:
        return json.loads(text), []
    except json.JSONDecodeError as exc:
        return None, [f"JSON 解析失败（第 {exc.lineno} 行第 {exc.colno} 列）: {exc.msg}"]


def semantic_chars(text: str) -> str:
    """Return characters that must survive punctuation-only speech splitting."""
    return "".join(SEMANTIC_RE.findall(text))


def effective_chars_per_sec(
    mode: str = "narration", playback_rate: float = DEFAULT_PLAYBACK_RATE
) -> float:
    """Return the planning clock; it is not a Seedance audio-speed parameter."""
    rate = playback_rate if mode == "narration" else 1.0
    return BASE_SPEECH_CHARS_PER_SEC * rate


def expected_target_sec(
    text: str,
    playback_rate: float = DEFAULT_PLAYBACK_RATE,
    mode: str = "narration",
) -> float:
    """Round one voice unit up to 0.1s without rounding every unit to whole seconds."""
    seconds = len(semantic_chars(text)) / effective_chars_per_sec(mode, playback_rate)
    return max(0.1, math.ceil((seconds - 1e-9) * 10) / 10)


def source_text_units(text: str) -> List[str]:
    """Use non-empty source lines as deterministic trace units."""
    return [line.strip() for line in text.splitlines() if line.strip()]


def _validate_id_sequence(records: Any, prefix: str, label: str, errors: List[str]) -> None:
    if not isinstance(records, list):
        errors.append(f"{label} 必须是数组")
        return
    for index, record in enumerate(records, start=1):
        if not isinstance(record, dict):
            continue
        expected = f"{prefix}{index:03d}"
        if record.get("id") != expected:
            errors.append(f"{label}[{index - 1}].id 必须为 {expected}")


def _string_list(value: Any, location: str, errors: List[str]) -> List[str]:
    if not isinstance(value, list) or not value:
        errors.append(f"{location} 必须是非空字符串数组")
        return []
    if any(not isinstance(item, str) or not item for item in value):
        errors.append(f"{location} 只能包含非空字符串")
        return []
    if len(set(value)) != len(value):
        errors.append(f"{location} 不得包含重复 ID")
    return value


def validate_narration_plan(data: Any, source_text: Optional[str] = None) -> List[str]:
    errors: List[str] = []
    if not isinstance(data, dict):
        return ["JSON 顶层必须是对象"]
    if list(data.keys()) != TOP_LEVEL_FIELDS:
        errors.append(f"顶层字段顺序必须为 {', '.join(TOP_LEVEL_FIELDS)}")

    input_type = data.get("inputType")
    if input_type not in INPUT_TYPES:
        errors.append(f"inputType 只允许 {sorted(INPUT_TYPES)}")
    narration_mode = data.get("narrationMode")
    if narration_mode not in NARRATION_MODES:
        errors.append(f"narrationMode 只允许 {sorted(NARRATION_MODES)}")
    fidelity_mode = data.get("fidelityMode")
    if fidelity_mode not in FIDELITY_MODES:
        errors.append(f"fidelityMode 只允许 {sorted(FIDELITY_MODES)}")
    elif input_type in {"screenplay", "short_story"} and fidelity_mode != "condensed":
        errors.append("screenplay/short_story 的 fast_recap 必须使用 condensed 改编")

    base_speech_rate = data.get("baseSpeechCharsPerSec")
    if base_speech_rate != BASE_SPEECH_CHARS_PER_SEC:
        errors.append(
            f"baseSpeechCharsPerSec 必须固定为本 skill 的规划基础值 "
            f"{BASE_SPEECH_CHARS_PER_SEC}"
        )
    playback_rate = data.get("playbackRate")
    if (
        not isinstance(playback_rate, (int, float))
        or isinstance(playback_rate, bool)
        or float(playback_rate) not in ALLOWED_PLAYBACK_RATES
    ):
        errors.append(
            f"playbackRate 只允许 {sorted(ALLOWED_PLAYBACK_RATES)}"
        )
        playback_rate = DEFAULT_PLAYBACK_RATE
    else:
        playback_rate = float(playback_rate)
    if data.get("genreProfile") not in GENRE_PROFILES:
        errors.append(f"genreProfile 只允许 {sorted(GENRE_PROFILES)}")

    plan_source = data.get("sourceText")
    if not isinstance(plan_source, str) or not plan_source.strip():
        errors.append("sourceText 必须是非空字符串")
        plan_source = ""
    if source_text is not None and plan_source != source_text:
        errors.append("sourceText 必须与 --source 文件逐字一致")

    source_units = data.get("sourceUnits")
    narration_units = data.get("narrationUnits")
    _validate_id_sequence(source_units, "S", "sourceUnits", errors)
    _validate_id_sequence(narration_units, "N", "narrationUnits", errors)
    if not isinstance(source_units, list) or not source_units:
        errors.append("sourceUnits 不得为空")
        source_units = []
    if not isinstance(narration_units, list) or not narration_units:
        errors.append("narrationUnits 不得为空")
        narration_units = []

    expected_source_units = source_text_units(plan_source)
    if len(source_units) != len(expected_source_units):
        errors.append(
            f"sourceUnits 数量 {len(source_units)} 与 sourceText 非空行数量 {len(expected_source_units)} 不一致"
        )

    source_to_narration: dict[str, List[str]] = {}
    for index, record in enumerate(source_units):
        location = f"sourceUnits[{index}]"
        if not isinstance(record, dict):
            errors.append(f"{location} 必须是对象")
            continue
        if list(record.keys()) != SOURCE_UNIT_FIELDS:
            errors.append(f"{location} 字段顺序必须为 {', '.join(SOURCE_UNIT_FIELDS)}")
        unit_id = record.get("id")
        text = record.get("text")
        if not isinstance(text, str) or not text:
            errors.append(f"{location}.text 必须是非空字符串")
        elif index < len(expected_source_units) and text != expected_source_units[index]:
            errors.append(f"{location}.text 必须按原顺序逐字对应 sourceText 非空行")
        ids = _string_list(record.get("narrationUnitIds"), f"{location}.narrationUnitIds", errors)
        if isinstance(unit_id, str):
            source_to_narration[unit_id] = ids

    narration_to_source: dict[str, List[str]] = {}
    primary_hook_indexes: List[int] = []
    for index, record in enumerate(narration_units):
        location = f"narrationUnits[{index}]"
        if not isinstance(record, dict):
            errors.append(f"{location} 必须是对象")
            continue
        if list(record.keys()) != NARRATION_UNIT_FIELDS:
            errors.append(f"{location} 字段顺序必须为 {', '.join(NARRATION_UNIT_FIELDS)}")
        unit_id = record.get("id")
        speaker = record.get("speaker")
        mode = record.get("mode")
        text = record.get("text")
        if not isinstance(speaker, str) or not speaker.strip():
            errors.append(f"{location}.speaker 必须是非空字符串")
        if mode not in VOICE_MODES:
            errors.append(f"{location}.mode 只允许 {sorted(VOICE_MODES)}")
        if mode == "narration" and speaker not in ANONYMOUS_NARRATORS:
            errors.append(f"{location} 匿名解说 speaker 必须为“解说”或“旁白”")
        if mode == "dialogue" and speaker in ANONYMOUS_NARRATORS | SYSTEM_SPEAKERS:
            errors.append(f"{location} 解说/旁白不能使用 dialogue，系统声也不能伪装角色对白")
        if mode in {"os", "vo"} and speaker in ANONYMOUS_NARRATORS | SYSTEM_SPEAKERS:
            errors.append(f"{location} {mode} 必须绑定具名角色")
        if mode == "system" and speaker not in SYSTEM_SPEAKERS:
            errors.append(f"{location} system 必须使用匿名系统声源")
        if not isinstance(text, str) or not text.strip():
            errors.append(f"{location}.text 必须是非空字符串")
        else:
            clean_text = text.strip()
            if not SPEECH_END_RE.search(clean_text):
                errors.append(f"{location}.text 必须以完整话语标点结束")
            semantic_length = len(semantic_chars(clean_text))
            if semantic_length == 0:
                errors.append(f"{location}.text 必须包含可发声的语义字符")
            max_semantic_chars = (
                MAX_NARRATION_SEMANTIC_CHARS
                if mode == "narration"
                else MAX_OTHER_VOICE_SEMANTIC_CHARS
            )
            if semantic_length > max_semantic_chars:
                errors.append(
                    f"{location}.text 超过 {max_semantic_chars} 个语义字符，"
                    "应按自然意群拆分"
                )
        source_ids = _string_list(record.get("sourceUnitIds"), f"{location}.sourceUnitIds", errors)
        if isinstance(unit_id, str):
            narration_to_source[unit_id] = source_ids

        target_sec = record.get("targetSec")
        if (
            not isinstance(target_sec, (int, float))
            or isinstance(target_sec, bool)
            or target_sec <= 0
        ):
            errors.append(f"{location}.targetSec 必须是大于 0 的秒数，最多保留一位小数")
        elif isinstance(text, str) and semantic_chars(text):
            expected = expected_target_sec(text, playback_rate, str(mode))
            if not math.isclose(float(target_sec), expected, abs_tol=1e-9):
                clock = effective_chars_per_sec(str(mode), playback_rate)
                errors.append(
                    f"{location}.targetSec 必须按基础每秒 {BASE_SPEECH_CHARS_PER_SEC} 字"
                    f"和当前 {playback_rate:g} 规划倍率（规划每秒 {clock:g} 字）"
                    f"确定性计算为 {expected} 秒，实际为 {target_sec} 秒"
                )

        pause_after_sec = record.get("pauseAfterSec")
        if pause_after_sec not in PAUSE_AFTER_SECONDS:
            errors.append(f"{location}.pauseAfterSec 只允许 0 或 0.5 秒")
        elif pause_after_sec == 0.5:
            boundary_text = "".join(
                str(record.get(field, ""))
                for field in ("visualIntent", "visualEvidence", "spoilerBoundary")
            )
            if (
                mode != "dialogue"
                and record.get("visualMode") != "transition"
                and PAUSE_BOUNDARY_RE.search(boundary_text) is None
            ):
                errors.append(
                    f"{location}.pauseAfterSec=0.5 只用于反转／揭晓、重大结果、"
                    "对白交接或时空切换；普通连续解说必须为 0"
                )
        hook_role = record.get("hookRole")
        if hook_role not in HOOK_ROLES:
            errors.append(f"{location}.hookRole 只允许 {sorted(HOOK_ROLES)}")
        elif hook_role == "primary":
            primary_hook_indexes.append(index)

        if record.get("visualMode") not in VISUAL_MODES:
            errors.append(f"{location}.visualMode 只允许 {sorted(VISUAL_MODES)}")
        for field in ("visualIntent", "visualEvidence", "spoilerBoundary", "scene"):
            if not isinstance(record.get(field), str) or not record[field].strip():
                errors.append(f"{location}.{field} 必须是非空字符串")

    known_sources = set(source_to_narration)
    known_narration = set(narration_to_source)
    for source_id, narration_ids in source_to_narration.items():
        for narration_id in narration_ids:
            if narration_id not in known_narration:
                errors.append(f"{source_id} 引用了不存在的 {narration_id}")
            elif source_id not in narration_to_source[narration_id]:
                errors.append(f"{source_id} 与 {narration_id} 的映射必须双向一致")
    for narration_id, source_ids in narration_to_source.items():
        for source_id in source_ids:
            if source_id not in known_sources:
                errors.append(f"{narration_id} 引用了不存在的 {source_id}")
            elif narration_id not in source_to_narration[source_id]:
                errors.append(f"{narration_id} 与 {source_id} 的映射必须双向一致")

    source_positions = {source_id: index for index, source_id in enumerate(source_to_narration)}
    previous_source_position = -1
    for narration_id, source_ids in narration_to_source.items():
        positions = [source_positions[source_id] for source_id in source_ids if source_id in source_positions]
        if positions != sorted(positions) or any(
            right != left + 1 for left, right in zip(positions, positions[1:])
        ):
            errors.append(f"{narration_id}.sourceUnitIds 必须按原文顺序引用相邻源单元")
        if positions and positions[-1] < previous_source_position:
            errors.append("narrationUnits 必须保持原文顺序，不得回跳到更早的 sourceUnit")
        if positions:
            previous_source_position = max(previous_source_position, positions[-1])

    narration_positions = {
        narration_id: index for index, narration_id in enumerate(narration_to_source)
    }
    for source_id, narration_ids in source_to_narration.items():
        positions = [
            narration_positions[narration_id]
            for narration_id in narration_ids
            if narration_id in narration_positions
        ]
        if positions != sorted(positions):
            errors.append(f"{source_id}.narrationUnitIds 必须按 narrationUnits 顺序排列")

    if primary_hook_indexes != [0]:
        errors.append("fast_recap 必须且只能让 N001 使用 hookRole=primary，承担前三秒主钩子")

    return errors


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("json_file", type=Path, help="待校验的 narration_plan.json")
    parser.add_argument("--source", type=Path, help="可选的机器原文锚点 source.txt")
    return parser


def main(argv: Optional[Sequence[str]] = None) -> int:
    args = build_parser().parse_args(argv)
    data, errors = load_json(args.json_file)
    source_text: Optional[str] = None
    if args.source is not None:
        try:
            source_text = args.source.read_text(encoding="utf-8")
        except (OSError, UnicodeError) as exc:
            errors.append(f"无法读取原文文件 {args.source}: {exc}")
    if errors:
        for error in errors:
            print(f"ERROR: {error}", file=sys.stderr)
        return 2
    validation_errors = validate_narration_plan(data, source_text)
    if validation_errors:
        for error in validation_errors:
            print(f"ERROR: {error}", file=sys.stderr)
        return 1
    print("OK: narration plan is valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
