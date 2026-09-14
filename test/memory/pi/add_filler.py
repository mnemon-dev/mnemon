#!/usr/bin/env python3
"""Add exactly N independent, topical, single-turn filler sessions per case.

Reads inputs only. Does not read the oracle, source turns, or question text to
compose filler. The original core is copied byte-for-value and never edited.
"""
import argparse
import copy
import hashlib
import json
from datetime import datetime, timedelta, timezone
from pathlib import Path


def filler_text(category, scope, digest, index):
    code = digest[:10].upper()
    entity = f'AUX-{code}'
    count = 1000 + index
    templates = {
        'cross_session_multihop': [
            f'独立书稿{entity}的审稿人是辅助审阅员{code}，其徽章编号为AUX-BADGE-{code}。',
            f'独立工作坊的徽章AUX-BADGE-{code}在柜AUX-DESK-{code}登记，不参加主档案工作坊。',
        ],
        'knowledge_update_history': [
            f'辅助项目{entity}的正式数据库为AuxDB-{code}，测试台为ProbeDB-{code}；本次只更新辅助项目记录。',
            f'辅助项目{entity}归档了一份旧配置快照，快照不是该项目的新切换。',
        ],
        'abstention_missing_fact': [
            f'辅助读书会{entity}的地点是辅助馆{code}，活动时间待定。',
            f'辅助读书会{entity}已安排独立签到员，未参与其它读书会。',
        ],
        'multilingual_negation': [
            f'For auxiliary project {entity}, sandbox exports are permitted; production exports require a separate review.',
            f'El proyecto auxiliar {entity} archivó su registro de exportación; esto no cambia ninguna política de otro proyecto.',
        ],
        'same_name_entity_isolation': [
            f'辅助采购员{code}负责订单AUX-ORDER-{code}，配送仓库为AUX-WH-{code}，分机为{count}。',
            f'辅助设备员{code}与辅助采购员{code}是两个不同的人，各自只管理这个独立档案内的订单。',
        ],
        'four_hop_alias_chain': [
            f'辅助陶样{entity}装入包裹AUX-PKG-{code}，承运单为AUX-WAY-{code}，目的地为辅助馆{code}。',
            f'辅助承运单AUX-WAY-{code}的扫描件已归档，归档没有创建新包裹。',
        ],
        'temporal_relative_date_arithmetic': [
            f'辅助合同{entity}在本会话前一天签署，本会话仅记录扫描件归档；没有描述其它合同的日期。',
            f'辅助交接{entity}本周仍在排期，原计划为暂定计划而非实际交接。',
        ],
        'future_effective_update_asof': [
            f'辅助支持合约{entity}级别为AuxTier-{code}，响应窗口为{count}分钟，本次公告只属于此辅助合约。',
            f'辅助支持合约{entity}的升级仍待独立签署，没有改变任何其它项目的生效日。',
        ],
        'negation_multiple_predicates': [
            f'辅助项目{entity}的生产部署受限，沙箱演练已获批准；这些权限仅属于辅助项目。',
            f'辅助项目{entity}完成了备份审查，尚未为辅助外发建立新的审批单。',
        ],
        'correction_retraction_history': [
            f'辅助设备{entity}的告警阈值为{count}，机壳编号AUX-CASE-{code}；两字段分别记录。',
            f'辅助设备{entity}撤销了本档案内的一次外壳换色申请，没有修改其它设备阈值。',
        ],
        'abstention_false_booking_premise': [
            f'辅助访客{code}为独立展会{entity}保留了旅馆意向，尚未付款。',
            f'辅助访客{code}的独立旅馆订单确认号为AUX-CONF-{code}，该订单不属于主档案中的任何访客。',
        ],
        'speaker_proposal_vs_commitment': [
            f'辅助演示{entity}的草案色板编号为AUX-PALETTE-{code}，还没有最终批准。',
            f'辅助演示{entity}只调整了字距，样例配色仅供辅助团队内部比较。',
        ],
        'cross_language_alias_and_update': [
            f'Le projet auxiliaire {entity} a le fournisseur AUX-SUP-{code}; il ne partage aucun responsable avec le dossier principal.',
            f'Для вспомогательного проекта {entity} контакт имеет код AUX-PERSON-{code}; это отдельный проект.',
            f'El proyecto auxiliar {entity} mantiene su alias AUX-ALIAS-{code}; no es un alias del proyecto principal.',
        ],
        'enumeration_dedup_and_refund': [
            f'辅助工作坊{entity}购买了AUX-KIT-{code}，实付{count}元；这不是主档案的采购。',
            f'辅助工作坊{entity}重发了AUX-RECEIPT-{code}，属于同一辅助订单的副本。',
        ],
        'conditional_permission_time_window': [
            f'辅助项目{entity}仅在自己的工单AUX-TICKET-{code}批准后开放沙箱，不能向其它项目借用权限。',
            f'辅助项目{entity}的临时窗口尚待独立审批，未声明其它项目的生产窗口。',
        ],
        'causal_requirement_multihop': [
            f'辅助图册{entity}要求可检索元数据，内部方案AUX-PROFILE-{code}满足该辅助要求。',
            f'辅助图册{entity}的方案别名为AUX-NAME-{code}，这是此辅助档案内的别名。',
        ],
    }
    choices = templates[category]
    return f'[独立档案scope={scope}；与主档案及其它辅助档案无关联] ' + choices[index % len(choices)]


def expand(inputs, records, case_ids, seed):
    result = copy.deepcopy(inputs)
    result['cases'] = [c for c in result['cases'] if not case_ids or c['id'] in case_ids]
    if case_ids - {c['id'] for c in result['cases']}:
        raise ValueError('Unknown requested case ID')
    for case in result['cases']:
        original = copy.deepcopy(case['sessions'])
        if any('.f' in s['id'] for s in original):
            raise ValueError('Input already contains filler; use the original inputs')
        first = datetime.fromisoformat(original[0]['date_time'].replace('Z', '+00:00'))
        last = datetime.fromisoformat(original[-1]['date_time'].replace('Z', '+00:00'))
        span_us = int((last - first).total_seconds() * 1_000_000)
        if span_us <= records:
            raise ValueError('Core timeline too short for requested filler count')
        for index in range(1, records + 1):
            scope = f'aux-{case["id"]}-{index:05d}'
            digest = hashlib.sha256(f'{seed}:{scope}'.encode()).hexdigest()
            stamp = first + timedelta(microseconds=span_us * index // (records + 1))
            sid = f'{case["id"]}.f{index:05d}'
            case['sessions'].append(dict(id=sid, date_time=stamp.astimezone(timezone.utc).isoformat().replace('+00:00', 'Z'),
                                         turns=[dict(id=f'{sid}.t1', speaker='user',
                                                     text=filler_text(case['category'], scope, digest, index))]))
        case['sessions'].sort(key=lambda s: (datetime.fromisoformat(s['date_time'].replace('Z', '+00:00')), s['id']))
        assert [s for s in case['sessions'] if '.f' not in s['id']] == original
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inputs', type=Path, required=True)
    parser.add_argument('--records', type=int, choices=[30, 120, 500], required=True,
                        help='Extra single-turn records/sessions PER selected case')
    parser.add_argument('--cases', nargs='*', default=[])
    parser.add_argument('--seed', default='mnemon-memory-regression-v1')
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    inputs = json.loads(args.inputs.read_text(encoding='utf-8'))
    result = expand(inputs, args.records, set(args.cases), args.seed)
    raw = (json.dumps(result, ensure_ascii=False, indent=2) + '\n').encode('utf-8')
    args.output.write_bytes(raw)
    print(json.dumps(dict(output=str(args.output), sha256=hashlib.sha256(raw).hexdigest(),
                          selected_cases=len(result['cases']), filler_records_per_case=args.records,
                          sessions=sum(len(c['sessions']) for c in result['cases']),
                          turns=sum(len(s['turns']) for c in result['cases'] for s in c['sessions'])), indent=2))


if __name__ == '__main__':
    main()
