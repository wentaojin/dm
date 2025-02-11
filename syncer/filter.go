// Copyright 2019 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package syncer

import (
	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/pingcap/parser/ast"
	bf "github.com/pingcap/tidb-tools/pkg/binlog-filter"
	"github.com/pingcap/tidb-tools/pkg/filter"

	"github.com/pingcap/dm/pkg/terror"
	"github.com/pingcap/dm/pkg/utils"
	onlineddl "github.com/pingcap/dm/syncer/online-ddl-tools"
)

// skipQuery will return true when
// - given `sql` matches builtin pattern.
// - any schema of table names is system schema.
// - any table name doesn't pass block-allow list.
// - type of SQL doesn't pass binlog filter.
func (s *Syncer) skipQuery(tables []*filter.Table, stmt ast.StmtNode, sql string) (bool, error) {
	if utils.IsBuildInSkipDDL(sql) {
		return true, nil
	}

	for _, table := range tables {
		if filter.IsSystemSchema(table.Schema) {
			return true, nil
		}
	}

	if len(tables) > 0 {
		tbs := s.baList.Apply(tables)
		if len(tbs) != len(tables) {
			return true, nil
		}
	}

	if s.binlogFilter == nil {
		return false, nil
	}

	et := bf.NullEvent
	if stmt != nil {
		et = bf.AstToDDLEvent(stmt)
	}
	if len(tables) == 0 {
		action, err := s.binlogFilter.Filter("", "", et, sql)
		if err != nil {
			return false, terror.Annotatef(terror.ErrSyncerUnitBinlogEventFilter.New(err.Error()), "skip query %s", sql)
		}

		if action == bf.Ignore {
			return true, nil
		}
	}

	for _, table := range tables {
		action, err := s.binlogFilter.Filter(table.Schema, table.Name, et, sql)
		if err != nil {
			return false, terror.Annotatef(terror.ErrSyncerUnitBinlogEventFilter.New(err.Error()), "skip query %s on `%v`", sql, table)
		}

		if action == bf.Ignore {
			return true, nil
		}
	}

	return false, nil
}

func (s *Syncer) skipDMLEvent(table *filter.Table, eventType replication.EventType) (bool, error) {
	schemaName, tableName := table.Schema, table.Name
	if filter.IsSystemSchema(schemaName) {
		return true, nil
	}

	tbs := []*filter.Table{table}
	tbs = s.baList.Apply(tbs)
	if len(tbs) == 0 {
		return true, nil
	}
	// filter ghost table
	if s.onlineDDL != nil {
		tp := s.onlineDDL.TableType(tableName)
		if tp != onlineddl.RealTable {
			return true, nil
		}
	}

	if s.binlogFilter == nil {
		return false, nil
	}

	var et bf.EventType
	switch eventType {
	case replication.WRITE_ROWS_EVENTv0, replication.WRITE_ROWS_EVENTv1, replication.WRITE_ROWS_EVENTv2:
		et = bf.InsertEvent
	case replication.UPDATE_ROWS_EVENTv0, replication.UPDATE_ROWS_EVENTv1, replication.UPDATE_ROWS_EVENTv2:
		et = bf.UpdateEvent
	case replication.DELETE_ROWS_EVENTv0, replication.DELETE_ROWS_EVENTv1, replication.DELETE_ROWS_EVENTv2:
		et = bf.DeleteEvent
	default:
		return false, terror.ErrSyncerUnitInvalidReplicaEvent.Generate(eventType)
	}

	action, err := s.binlogFilter.Filter(schemaName, tableName, et, "")
	if err != nil {
		return false, terror.Annotatef(terror.ErrSyncerUnitBinlogEventFilter.New(err.Error()), "skip row event %s on `%v`", eventType, table)
	}

	return action == bf.Ignore, nil
}
